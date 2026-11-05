package player

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type limitedStderr struct {
	mu   sync.Mutex
	data []byte
}

func (b *limitedStderr) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.data = append(b.data, p...)
	if len(b.data) > 4096 {
		b.data = append([]byte(nil), b.data[len(b.data)-4096:]...)
	}
	return n, nil
}
func (b *limitedStderr) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.data))
}

type ffmpegProcess struct {
	cmd    *exec.Cmd
	out    *os.File
	done   chan struct{}
	err    error
	stderr *limitedStderr
}

func (p *ffmpegProcess) close() {
	p.out.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	<-p.done
}

type FFmpegDecoder struct {
	mu                   sync.Mutex
	ctx                  context.Context
	mediaURL             string
	headers              map[string]string
	sampleRate, channels int
	totalSec             float64
	curFrame             int64
	process              *ffmpegProcess
	pending              []byte
	closed               bool
	gen                  uint64
}

func NewFFmpegDecoder(raw string, headers map[string]string, total float64) (*FFmpegDecoder, error) {
	return NewFFmpegDecoderContext(context.Background(), raw, headers, total)
}
func NewFFmpegDecoderContext(ctx context.Context, raw string, headers map[string]string, total float64) (*FFmpegDecoder, error) {
	copied := make(map[string]string, len(headers))
	for k, v := range headers {
		copied[k] = v
	}
	d := &FFmpegDecoder{ctx: ctx, mediaURL: raw, headers: copied, totalSec: total, sampleRate: 44100, channels: 2}
	if err := d.startAt(0); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
func isRemoteURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https")
}
func headerString(headers map[string]string) string {
	var b strings.Builder
	for k, v := range headers {
		b.WriteString(k + ": " + v + "\r\n")
	}
	return b.String()
}
func (d *FFmpegDecoder) startAt(sec float64) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return fmt.Errorf("decoder closed")
	}
	d.gen++
	gen := d.gen
	old := d.process
	d.process = nil
	d.pending = nil
	d.mu.Unlock()
	if old != nil {
		old.close()
	}
	if err := d.ctx.Err(); err != nil {
		return err
	}
	args := []string{"-nostdin"}
	if len(d.headers) > 0 && isRemoteURL(d.mediaURL) {
		args = append(args, "-headers", headerString(d.headers))
	}
	if sec > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", sec))
	}
	args = append(args, "-i", d.mediaURL, "-f", "f32le", "-ac", "2", "-ar", "44100", "-loglevel", "error", "pipe:1")
	cmd := exec.CommandContext(d.ctx, "ffmpeg", args...)
	cmd.WaitDelay = 250 * time.Millisecond
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	stderr := &limitedStderr{}
	cmd.Stdout = writer
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		return err
	}
	writer.Close()
	process := &ffmpegProcess{cmd: cmd, out: reader, done: make(chan struct{}), stderr: stderr}
	go func() { process.err = cmd.Wait(); close(process.done) }()
	d.mu.Lock()
	if d.closed || gen != d.gen {
		d.mu.Unlock()
		process.close()
		return context.Canceled
	}
	d.process = process
	d.mu.Unlock()
	// Confirm at least one stereo frame and keep it for the first decoder read.
	first := make([]byte, 8)
	ready := make(chan error, 1)
	go func() { _, e := io.ReadFull(reader, first); ready <- e }()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case err = <-ready:
		if err != nil {
			process.close()
			return fmt.Errorf("ffmpeg produced no PCM: %v: %s", err, stderr.String())
		}
	case <-d.ctx.Done():
		d.Close()
		<-ready
		return d.ctx.Err()
	case <-timer.C:
		d.Close()
		<-ready
		return fmt.Errorf("ffmpeg PCM initialization timed out")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || gen != d.gen {
		return context.Canceled
	}
	d.pending = first
	d.curFrame = int64(sec * float64(d.sampleRate))
	return nil
}
func (d *FFmpegDecoder) readGeneration() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.gen
}

// interruptRead unblocks an in-flight read after the player queues a seek.
func (d *FFmpegDecoder) interruptRead(gen uint64) {
	d.mu.Lock()
	if d.closed || d.gen != gen {
		d.mu.Unlock()
		return
	}
	d.gen++
	p := d.process
	d.process = nil
	d.mu.Unlock()
	if p != nil {
		go p.close()
	}
}

func (d *FFmpegDecoder) SampleRate() int { return d.sampleRate }
func (d *FFmpegDecoder) Channels() int   { return d.channels }
func (d *FFmpegDecoder) Len() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.totalSec > 0 {
		return int64(d.totalSec * float64(d.sampleRate))
	}
	return -1
}
func (d *FFmpegDecoder) SetDuration(sec float64) {
	if sec > 0 {
		d.mu.Lock()
		d.totalSec = sec
		d.mu.Unlock()
	}
}
func (d *FFmpegDecoder) Read(dst []float32) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	raw := make([]byte, len(dst)*4)
	for attempt := 0; attempt < 16; attempt++ {
		d.mu.Lock()
		p, gen, closed := d.process, d.gen, d.closed
		prefix := d.pending
		d.pending = nil
		d.mu.Unlock()
		if closed {
			return 0, io.EOF
		}
		if p == nil {
			return 0, errReadInterrupted
		}
		n, err := io.ReadFull(io.MultiReader(bytes.NewReader(prefix), p.out), raw)
		d.mu.Lock()
		stale := d.gen != gen
		closed = d.closed
		d.mu.Unlock()
		if stale {
			return 0, errReadInterrupted
		}
		if closed {
			return 0, io.EOF
		}
		count := n / 4
		count -= count % d.channels
		for i := 0; i < count; i++ {
			dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		}
		d.mu.Lock()
		if d.gen != gen || d.closed {
			d.mu.Unlock()
			return 0, errReadInterrupted
		}
		d.curFrame += int64(count / d.channels)
		d.mu.Unlock()
		if err != nil && count == 0 {
			<-p.done
			if p.err != nil {
				return 0, fmt.Errorf("ffmpeg decode failed: %w: %s", p.err, p.stderr.String())
			}
			return 0, io.EOF
		}
		return count, nil
	}
	return 0, context.Canceled
}
func (d *FFmpegDecoder) SetPosition(frame int64) error {
	return d.startAt(float64(max(frame, 0)) / float64(d.sampleRate))
}
func (d *FFmpegDecoder) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.gen++
	p := d.process
	d.process = nil
	d.mu.Unlock()
	if p != nil {
		p.close()
	}
	return nil
}

// ProbeDuration asks ffprobe how long a media file or stream is, in
// seconds. It returns 0 when ffprobe is missing or the media cannot be read.
func ProbeDuration(mediaURL string, headers map[string]string) float64 {
	return ProbeDurationContext(context.Background(), mediaURL, headers)
}
func ProbeDurationContext(parent context.Context, mediaURL string, headers map[string]string) float64 {
	if mediaURL == "" {
		return 0
	}

	args := []string{}
	if len(headers) > 0 && isRemoteURL(mediaURL) {
		args = append(args, "-headers", headerString(headers))
	}
	args = append(args,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-i", mediaURL,
	)

	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	cmd.WaitDelay = 250 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	var parsed struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return 0
	}
	dur, err := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64)
	if err != nil || dur <= 0 || dur > 24*3600 {
		return 0
	}
	return dur
}
