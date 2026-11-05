package polyfill

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/dop251/goja"
)

// CreateCryptoJSModule creates the 'crypto-js' module
func CreateCryptoJSModule(vm *goja.Runtime) *goja.Object {
	cryptoJS := vm.NewObject()

	// WordArray helpers
	var createWordArray func(data []byte) *goja.Object
	var getBytesFromValue func(v goja.Value) []byte

	createWordArray = func(data []byte) *goja.Object {
		obj := vm.NewObject()
		// Store raw bytes in a hidden or exported field
		_ = obj.Set("__bytes", data)
		_ = obj.Set("sigBytes", len(data))

		// words array for crypto-js compatibility (each word is 32-bit big-endian)
		words := make([]int64, (len(data)+3)/4)
		for i := 0; i < len(data); i++ {
			words[i/4] |= int64(data[i]) << (24 - 8*(i%4))
		}
		_ = obj.Set("words", words)

		_ = obj.Set("toString", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) {
				encObj := call.Arguments[0].ToObject(vm)
				stringify := encObj.Get("stringify")
				if fn, ok := goja.AssertFunction(stringify); ok {
					res, err := fn(encObj, obj)
					if err == nil {
						return res
					}
				}
			}
			// Default is hex
			return vm.ToValue(hex.EncodeToString(getBytesFromValue(obj)))
		})
		return obj
	}

	getBytesFromValue = func(v goja.Value) []byte {
		if v == nil || goja.IsNull(v) || goja.IsUndefined(v) {
			return nil
		}
		if obj, ok := v.(*goja.Object); ok {
			if wordsVal := obj.Get("words"); wordsVal != nil && !goja.IsUndefined(wordsVal) && !goja.IsNull(wordsVal) {
				words := wordsVal.ToObject(vm)
				length := int(words.Get("length").ToInteger())
				sigBytes := length * 4
				if sigVal := obj.Get("sigBytes"); sigVal != nil && !goja.IsUndefined(sigVal) {
					sigBytes = int(sigVal.ToInteger())
				}
				if sigBytes < 0 {
					sigBytes = 0
				}
				res := make([]byte, sigBytes)
				for i := range res {
					if i/4 < length {
						if value := words.Get(fmt.Sprint(i / 4)); value != nil && !goja.IsUndefined(value) {
							word := uint32(value.ToInteger())
							res[i] = byte(word >> uint(24-8*(i%4)))
						}
					}
				}
				return res
			}
			if raw := obj.Get("__bytes"); raw != nil && !goja.IsUndefined(raw) {
				if b, ok := raw.Export().([]byte); ok {
					return b
				}
			}
		}
		// String fallback
		return []byte(v.String())
	}

	// enc object
	enc := vm.NewObject()

	// Utf8
	utf8Enc := vm.NewObject()
	_ = utf8Enc.Set("parse", func(s string) *goja.Object {
		return createWordArray([]byte(s))
	})
	_ = utf8Enc.Set("stringify", func(wordArray goja.Value) string {
		return string(getBytesFromValue(wordArray))
	})
	_ = enc.Set("Utf8", utf8Enc)

	// Hex
	hexEnc := vm.NewObject()
	_ = hexEnc.Set("parse", func(s string) *goja.Object {
		b, _ := hex.DecodeString(s)
		return createWordArray(b)
	})
	_ = hexEnc.Set("stringify", func(wordArray goja.Value) string {
		return hex.EncodeToString(getBytesFromValue(wordArray))
	})
	_ = enc.Set("Hex", hexEnc)

	// Base64
	base64Enc := vm.NewObject()
	_ = base64Enc.Set("parse", func(s string) *goja.Object {
		b, _ := base64.StdEncoding.DecodeString(s)
		return createWordArray(b)
	})
	_ = base64Enc.Set("stringify", func(wordArray goja.Value) string {
		return base64.StdEncoding.EncodeToString(getBytesFromValue(wordArray))
	})
	_ = enc.Set("Base64", base64Enc)

	// Latin1
	latin1Enc := vm.NewObject()
	_ = latin1Enc.Set("parse", func(s string) *goja.Object {
		b := make([]byte, len(s))
		for i := 0; i < len(s); i++ {
			b[i] = byte(s[i])
		}
		return createWordArray(b)
	})
	_ = latin1Enc.Set("stringify", func(wordArray goja.Value) string {
		data := getBytesFromValue(wordArray)
		return string(data)
	})
	_ = enc.Set("Latin1", latin1Enc)

	_ = cryptoJS.Set("enc", enc)

	// Hashing functions
	_ = cryptoJS.Set("MD5", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return createWordArray(nil)
		}
		data := getBytesFromValue(call.Arguments[0])
		sum := md5.Sum(data)
		return createWordArray(sum[:])
	})

	_ = cryptoJS.Set("SHA1", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return createWordArray(nil)
		}
		data := getBytesFromValue(call.Arguments[0])
		sum := sha1.Sum(data)
		return createWordArray(sum[:])
	})

	_ = cryptoJS.Set("SHA256", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return createWordArray(nil)
		}
		data := getBytesFromValue(call.Arguments[0])
		sum := sha256.Sum256(data)
		return createWordArray(sum[:])
	})

	_ = cryptoJS.Set("HmacMD5", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return createWordArray(nil)
		}
		data := getBytesFromValue(call.Arguments[0])
		key := getBytesFromValue(call.Arguments[1])
		mac := hmac.New(md5.New, key)
		mac.Write(data)
		return createWordArray(mac.Sum(nil))
	})

	_ = cryptoJS.Set("HmacSHA1", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return createWordArray(nil)
		}
		data := getBytesFromValue(call.Arguments[0])
		key := getBytesFromValue(call.Arguments[1])
		mac := hmac.New(sha1.New, key)
		mac.Write(data)
		return createWordArray(mac.Sum(nil))
	})

	_ = cryptoJS.Set("HmacSHA256", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return createWordArray(nil)
		}
		data := getBytesFromValue(call.Arguments[0])
		key := getBytesFromValue(call.Arguments[1])
		mac := hmac.New(sha256.New, key)
		mac.Write(data)
		return createWordArray(mac.Sum(nil))
	})

	// mode and pad constants
	modeObj := vm.NewObject()
	_ = modeObj.Set("CBC", "CBC")
	_ = modeObj.Set("ECB", "ECB")
	_ = modeObj.Set("CFB", "CFB")
	_ = modeObj.Set("OFB", "OFB")
	_ = modeObj.Set("CTR", "CTR")
	// Preserve known CryptoJS names so selecting an unsupported mode is rejected
	// instead of treating its undefined value as the default CBC mode.
	_ = modeObj.Set("CTRGladman", "CTRGladman")
	_ = cryptoJS.Set("mode", modeObj)

	padObj := vm.NewObject()
	_ = padObj.Set("Pkcs7", "Pkcs7")
	_ = padObj.Set("NoPadding", "NoPadding")
	_ = padObj.Set("ZeroPadding", "ZeroPadding")
	_ = padObj.Set("AnsiX923", "AnsiX923")
	_ = padObj.Set("Iso10126", "Iso10126")
	_ = padObj.Set("Iso97971", "Iso97971")
	_ = cryptoJS.Set("pad", padObj)

	// AES
	aesObj := vm.NewObject()
	_ = aesObj.Set("encrypt", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.NewObject()
		}
		plainBytes := getBytesFromValue(call.Arguments[0])
		keyBytes := getBytesFromValue(call.Arguments[1])
		_, passwordKey := call.Arguments[1].Export().(string)
		var salt []byte

		var ivBytes []byte
		mode := "CBC"
		padding := "Pkcs7"

		if len(call.Arguments) > 2 && !goja.IsUndefined(call.Arguments[2]) {
			cfg := call.Arguments[2].ToObject(vm)
			if saltVal := cfg.Get("salt"); saltVal != nil && !goja.IsUndefined(saltVal) && !goja.IsNull(saltVal) {
				salt = getBytesFromValue(saltVal)
			}
			if ivVal := cfg.Get("iv"); ivVal != nil && !goja.IsUndefined(ivVal) {
				ivBytes = getBytesFromValue(ivVal)
			}
			if modeVal := cfg.Get("mode"); modeVal != nil && !goja.IsUndefined(modeVal) {
				mode = modeVal.String()
			}
			if padVal := cfg.Get("padding"); padVal != nil && !goja.IsUndefined(padVal) {
				padding = padVal.String()
			}
		}

		if passwordKey {
			if salt == nil {
				salt = make([]byte, 8)
				if _, err := rand.Read(salt); err != nil {
					panic(vm.NewGoError(err))
				}
			}
			if len(salt) != 8 {
				panic(vm.NewGoError(fmt.Errorf("OpenSSL salt must be 8 bytes")))
			}
			keyBytes, ivBytes = opensslKeyIV(keyBytes, salt)
		}

		cipherBytes, err := aesEncrypt(plainBytes, keyBytes, ivBytes, mode, padding)
		if err != nil {
			panic(vm.NewGoError(err))
		}

		// CryptoJS AES.encrypt returns CipherParams whose toString() produces base64 ciphertext
		res := vm.NewObject()
		ciphertextWA := createWordArray(cipherBytes)
		_ = res.Set("ciphertext", ciphertextWA)
		_ = res.Set("key", createWordArray(keyBytes))
		_ = res.Set("iv", createWordArray(ivBytes))
		if passwordKey {
			_ = res.Set("salt", createWordArray(salt))
		}
		_ = res.Set("toString", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) {
				encObj := call.Arguments[0].ToObject(vm)
				stringify := encObj.Get("stringify")
				if fn, ok := goja.AssertFunction(stringify); ok {
					r, _ := fn(encObj, ciphertextWA)
					return r
				}
			}
			encoded := cipherBytes
			if passwordKey {
				encoded = append(append([]byte("Salted__"), salt...), cipherBytes...)
			}
			return vm.ToValue(base64.StdEncoding.EncodeToString(encoded))
		})
		return res
	})

	_ = aesObj.Set("decrypt", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return createWordArray(nil)
		}
		var cipherBytes, salt []byte
		// argument 0 might be a string (base64) or CipherParams object
		arg0 := call.Arguments[0]
		if obj, ok := arg0.(*goja.Object); ok {
			if saltVal := obj.Get("salt"); saltVal != nil && !goja.IsUndefined(saltVal) && !goja.IsNull(saltVal) {
				salt = getBytesFromValue(saltVal)
			}
			if ct := obj.Get("ciphertext"); ct != nil && !goja.IsUndefined(ct) {
				cipherBytes = getBytesFromValue(ct)
			} else {
				cipherBytes = getBytesFromValue(obj)
			}
		} else {
			// string: might be base64 or hex
			str := arg0.String()
			b, err := base64.StdEncoding.DecodeString(str)
			if err != nil {
				b, _ = hex.DecodeString(str)
			}
			cipherBytes = b
		}

		keyBytes := getBytesFromValue(call.Arguments[1])
		_, passwordKey := call.Arguments[1].Export().(string)
		if passwordKey && len(cipherBytes) >= 16 && bytes.Equal(cipherBytes[:8], []byte("Salted__")) {
			salt, cipherBytes = cipherBytes[8:16], cipherBytes[16:]
		}
		var ivBytes []byte
		mode := "CBC"
		padding := "Pkcs7"

		if len(call.Arguments) > 2 && !goja.IsUndefined(call.Arguments[2]) {
			cfg := call.Arguments[2].ToObject(vm)
			if ivVal := cfg.Get("iv"); ivVal != nil && !goja.IsUndefined(ivVal) {
				ivBytes = getBytesFromValue(ivVal)
			}
			if modeVal := cfg.Get("mode"); modeVal != nil && !goja.IsUndefined(modeVal) {
				mode = modeVal.String()
			}
			if padVal := cfg.Get("padding"); padVal != nil && !goja.IsUndefined(padVal) {
				padding = padVal.String()
			}
		}

		if passwordKey {
			if len(salt) != 8 {
				panic(vm.NewGoError(fmt.Errorf("missing or invalid OpenSSL salt")))
			}
			keyBytes, ivBytes = opensslKeyIV(keyBytes, salt)
		}
		plainBytes, err := aesDecrypt(cipherBytes, keyBytes, ivBytes, mode, padding)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return createWordArray(plainBytes)
	})

	_ = cryptoJS.Set("AES", aesObj)

	// lib.WordArray.create
	libObj := vm.NewObject()
	wordArrayClass := vm.NewObject()
	_ = wordArrayClass.Set("create", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return createWordArray(nil)
		}
		words := call.Arguments[0]
		if goja.IsUndefined(words) || goja.IsNull(words) {
			return createWordArray(nil)
		}
		obj := vm.NewObject()
		_ = obj.Set("words", words)
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) {
			_ = obj.Set("sigBytes", call.Arguments[1])
		}
		result := createWordArray(getBytesFromValue(obj))
		_ = result.Set("words", words)
		return result
	})
	_ = libObj.Set("WordArray", wordArrayClass)
	_ = cryptoJS.Set("lib", libObj)

	return cryptoJS
}

// OpenSSL EVP_BytesToKey with MD5, one iteration, AES-256 key and IV.
func opensslKeyIV(password, salt []byte) ([]byte, []byte) {
	material := make([]byte, 0, 48)
	var previous []byte
	for len(material) < 48 {
		h := md5.New()
		h.Write(previous)
		h.Write(password)
		h.Write(salt)
		previous = h.Sum(nil)
		material = append(material, previous...)
	}
	return material[:32], material[32:48]
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padtext...)
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padding := int(data[length-1])
	if padding > length || padding == 0 {
		return data, nil // Return as is rather than error
	}
	for i := length - padding; i < length; i++ {
		if int(data[i]) != padding {
			return data, nil
		}
	}
	return data[:length-padding], nil
}

func zeroPad(data []byte, blockSize int) []byte {
	if len(data)%blockSize == 0 {
		return data
	}
	padding := blockSize - len(data)%blockSize
	return append(data, make([]byte, padding)...)
}

func zeroUnpad(data []byte) []byte {
	return bytes.TrimRight(data, "\x00")
}

func aesEncrypt(plain, key, iv []byte, mode, padding string) ([]byte, error) {
	block, err := newAESBlock(key)
	if err != nil {
		return nil, err
	}
	if err := validateAESOptions(mode, padding); err != nil {
		return nil, err
	}

	blockSize := block.BlockSize()
	var padded []byte
	switch padding {
	case "NoPadding":
		padded = plain
	case "ZeroPadding":
		padded = zeroPad(plain, blockSize)
	case "Pkcs7":
		padded = pkcs7Pad(plain, blockSize)
	}
	return cryptAES(block, padded, iv, mode, false)
}

func aesDecrypt(ciphertext, key, iv []byte, mode, padding string) ([]byte, error) {
	block, err := newAESBlock(key)
	if err != nil {
		return nil, err
	}
	if err := validateAESOptions(mode, padding); err != nil {
		return nil, err
	}
	plain, err := cryptAES(block, ciphertext, iv, mode, true)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 {
		return plain, nil
	}
	switch padding {
	case "NoPadding":
		return plain, nil
	case "ZeroPadding":
		return zeroUnpad(plain), nil
	default:
		return pkcs7Unpad(plain)
	}
}

// Preserve the existing key normalization for plugins that use short keys.
func newAESBlock(key []byte) (cipher.Block, error) {
	size := 16
	if len(key) > 24 {
		size = 32
	} else if len(key) > 16 {
		size = 24
	}
	normalized := make([]byte, size)
	copy(normalized, key)
	return aes.NewCipher(normalized)
}

func validateAESOptions(mode, padding string) error {
	switch mode {
	case "CBC", "ECB", "CFB", "OFB", "CTR":
	default:
		return fmt.Errorf("unsupported AES mode: %s", mode)
	}
	switch padding {
	case "Pkcs7", "NoPadding", "ZeroPadding":
	default:
		return fmt.Errorf("unsupported AES padding: %s", padding)
	}
	return nil
}

func cryptAES(block cipher.Block, input, iv []byte, mode string, decrypt bool) ([]byte, error) {
	blockSize := block.BlockSize()
	if (mode == "ECB" || mode == "CBC") && len(input)%blockSize != 0 {
		return nil, fmt.Errorf("AES %s requires complete blocks", mode)
	}
	output := make([]byte, len(input))
	if mode == "ECB" {
		for i := 0; i < len(input); i += blockSize {
			if decrypt {
				block.Decrypt(output[i:i+blockSize], input[i:i+blockSize])
			} else {
				block.Encrypt(output[i:i+blockSize], input[i:i+blockSize])
			}
		}
		return output, nil
	}

	// Keep the existing zero-extension and truncation behavior for IVs.
	vector := make([]byte, blockSize)
	copy(vector, iv)
	switch mode {
	case "CBC":
		if decrypt {
			cipher.NewCBCDecrypter(block, vector).CryptBlocks(output, input)
		} else {
			cipher.NewCBCEncrypter(block, vector).CryptBlocks(output, input)
		}
	case "CFB":
		if decrypt {
			cipher.NewCFBDecrypter(block, vector).XORKeyStream(output, input)
		} else {
			cipher.NewCFBEncrypter(block, vector).XORKeyStream(output, input)
		}
	case "OFB":
		cipher.NewOFB(block, vector).XORKeyStream(output, input)
	case "CTR":
		// CryptoJS increments only the last 32-bit word, without carry.
		stream := make([]byte, blockSize)
		for offset := 0; offset < len(input); offset += blockSize {
			block.Encrypt(stream, vector)
			n := len(input) - offset
			if n > blockSize {
				n = blockSize
			}
			for i := 0; i < n; i++ {
				output[offset+i] = input[offset+i] ^ stream[i]
			}
			for i := blockSize - 1; i >= blockSize-4; i-- {
				vector[i]++
				if vector[i] != 0 {
					break
				}
			}
		}
	}
	return output, nil
}
