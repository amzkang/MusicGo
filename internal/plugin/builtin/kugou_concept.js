// 酷狗概念版音源插件（MusicGo）
//
// 扫码登录与播放地址走酷狗官方网关（概念版参数），搜索/榜单/歌词走公开端点。
// 登录方式：在 MusicGo 插件页点击「扫码登录」，用酷狗概念版 App 扫码；
// 成功后 token/userid 自动写入本插件的用户变量并持久化。
//
// 接口无官方文档，token 会过期：失效后重新扫码即可。

const axios = require("axios");
const CryptoJS = require("crypto-js");
const cheerio = require("cheerio");
const he = require("he");
const bigInteger = require("big-integer");
const lyricDecoder = require("musicgo/lyric");

// —— 概念版（lite）参数 ——
const APPID = 3116;
const CLIENTVER = 11440;
const ANDROID_SALT = "LnT6xpN3khm36zse0QzvmgTZ3waWdRSA";
const SIGNKEY_SALT = "185672dd44712f60bb1736df5a377e82";
const WEB_SALT = "NVPh5oo715z5DIWAeQlhMDsWXXQV4hwt";
const SRC_APPID = 2919;
// 申请二维码用的 appid（APP 端二维码固定 1001），二维码内容里再指向概念版
const QR_APPID = 1001;
const UA = "Android15-1070-11083-46-0-DiscoveryDRADProtocol-wifi";

// 设备标识：一份固定种子的 md5 十进制串，作为本机的 mid
const MID = bigInteger(CryptoJS.MD5("musicgo-kugou-concept-device").toString(), 16).toString();

// 概念版 usercenter RSA 公钥模数（get_my_info 的 p 参数需要）
const LITE_RSA_N = "c40a2d0da76511f3bb1cc2bbd3afbd8bea83b4d6b05b6c13eb8920c53f1af7679b32ba0d0edb843240ef1b836efed3ee240734c14c1399fd6594d16af22f52525d14d72e0155c6dcc8638d4f7bb94f3a0b1f4c29f991972f2a160a25eb0a9e724336be7f69bbd319ffab1c6dd8470b021dc434f3faba89f4a2a01b33bdbdd08b";

const PAGE_SIZE = 20;

const QUALITIES = {
  low: "128",
  standard: "320",
  high: "flac",
  super: "high",
  lossless: "flac",
};

const webHeaders = {
  "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/106.0.0.0 Safari/537.36",
  Accept: "*/*",
  "Accept-Language": "zh-CN,zh;q=0.9",
};

// —— 签名 ——
// web 签名：登录相关端点。对 "k=v" 整串按字典序排序后拼接（与官方 Web 客户端一致）
function webSignature(params) {
  const joined = Object.keys(params).map((k) => `${k}=${params[k]}`).sort().join("");
  return CryptoJS.MD5(WEB_SALT + joined + WEB_SALT).toString();
}

// android 签名：网关端点。POST 时请求体字符串也参与签名
function androidSignature(params, data) {
  const joined = Object.keys(params).map((k) => `${k}=${typeof params[k] === "object" ? JSON.stringify(params[k]) : params[k]}`).sort().join("");
  return CryptoJS.MD5(ANDROID_SALT + joined + (data || "") + ANDROID_SALT).toString();
}

function signKeyFor(hash, userid) {
  return CryptoJS.MD5(hash + SIGNKEY_SALT + APPID + MID + userid).toString();
}

// 零填充裸 RSA：JSON 序列化 → UTF-8 字节 → 右侧补零到 128 字节 → modPow(e=65537)
// → hex 补齐 256 位 → 大写。与官方 cryptoRSAEncrypt 对齐（非 PKCS1）
function liteRsaHex(obj) {
  const s = JSON.stringify(obj);
  const bytes = [];
  for (let i = 0; i < s.length; i++) {
    const code = s.charCodeAt(i);
    if (code < 0x80) bytes.push(code);
    else if (code < 0x800) bytes.push(0xc0 | (code >> 6), 0x80 | (code & 63));
    else bytes.push(0xe0 | (code >> 12), 0x80 | ((code >> 6) & 63), 0x80 | (code & 63));
  }
  while (bytes.length < 128) bytes.push(0);
  const hex = bytes.map((b) => (b < 16 ? "0" : "") + b.toString(16)).join("");
  const n = bigInteger(LITE_RSA_N, 16);
  const e = bigInteger("10001", 16);
  const enc = bigInteger(hex, 16).modPow(e, n).toString(16);
  return enc.padStart(256, "0").toUpperCase();
}

function clientTime() {
  return Math.floor(Date.now() / 1000);
}

// 登录端点请求：网关封装会给每个请求注入设备默认参数（dfid/mid/uuid/
// clientver/clienttime）再签名，这里对齐同样的参数集，否则返回 20010
const KG_HEADERS = {
  "kg-rc": "1",
  "kg-thash": "5d816a0",
  "kg-rec": "1",
  "kg-rf": "B9EDA08A64250DEFFBCADDEE00F8F25F",
  "User-Agent": UA,
  Accept: "*/*",
};

async function loginGet(path, moduleParams) {
  const params = Object.assign({
    dfid: "-",
    mid: MID,
    uuid: "-",
    appid: APPID,
    clientver: CLIENTVER,
    clienttime: clientTime(),
  }, moduleParams);
  params.signature = webSignature(params);
  const headers = Object.assign({}, KG_HEADERS, {
    dfid: params.dfid,
    mid: params.mid,
    clienttime: String(params.clienttime),
  });
  return (await axios.get("https://login-user.kugou.com" + path, {
    params,
    headers,
    timeout: 10000,
  })).data;
}

// —— 扫码登录 ——
async function getLoginQR() {
  const qrPage = `https://h5.kugou.com/apps/loginQRCode/html/index.html?appid=${APPID}&`;
  const res = await loginGet("/v2/qrcode", {
    appid: QR_APPID,
    type: 1,
    plat: 4,
    qrcode_txt: qrPage,
    srcappid: SRC_APPID,
  });
  const key = res.data && res.data.qrcode;
  if (!key) {
    throw new Error(`接口错误码 ${res.error_code}`);
  }
  return {
    qrText: `https://h5.kugou.com/apps/loginQRCode/html/index.html?qrcode=${key}`,
    state: key,
    message: "打开酷狗概念版 App 扫码登录",
  };
}

async function pollLoginQR(state) {
  const res = await loginGet("/v2/get_userinfo_qrcode", {
    plat: 4,
    appid: APPID,
    srcappid: SRC_APPID,
    qrcode: state,
  });
  const data = res.data || {};
  if (data.status === 4) {
    return {
      status: "confirmed",
      userVariables: {
        token: String(data.token),
        userid: String(data.userid),
        nickname: data.nick_name || data.nickname || "",
        avatar: profileAvatar(data),
      },
    };
  }
  if (data.status === 2) return { status: "scanned" };
  if (data.status === 1) return { status: "waiting" };
  return { status: "expired" };
}

function getLoginStatus() {
  const vars = env.getUserVariables();
  if (vars.token && vars.userid) {
    return { loggedIn: true, message: "已登录" };
  }
  return { loggedIn: false };
}

// —— 播放地址（概念版网关；登录后可解锁会员音质）——
// 网关 POST：默认参数 + token 与签名（请求体字符串参与签名）
async function gatewayPost(path, query, body, extraHeaders) {
  const vars = env.getUserVariables();
  const params = Object.assign({
    dfid: "-",
    mid: MID,
    uuid: "-",
    appid: APPID,
    clientver: CLIENTVER,
    clienttime: clientTime(),
  }, query);
  if (vars.token) params.token = vars.token;
  if (vars.userid) params.userid = vars.userid;
  const dataStr = body ? JSON.stringify(body) : "";
  params.signature = androidSignature(params, dataStr);
  const headers = Object.assign({
    dfid: params.dfid,
    mid: params.mid,
    clienttime: String(params.clienttime),
  }, KG_HEADERS, extraHeaders || {});
  if (body && headers["content-type"] === undefined) {
    headers["content-type"] = "application/json;charset=utf-8";
  }
  return (await axios.post("https://gateway.kugou.com" + path, dataStr, {
    params,
    headers,
    timeout: 15000,
  })).data;
}

async function gatewayGet(path, query, router) {
  const vars = env.getUserVariables();
  const params = Object.assign({
    dfid: "-", mid: MID, uuid: "-", appid: APPID,
    clientver: CLIENTVER, clienttime: clientTime(), userid: vars.userid || 0,
  }, query);
  if (vars.token) params.token = vars.token;
  params.signature = androidSignature(params);
  const res = (await axios.get("https://gateway.kugou.com" + path, {
    params,
    headers: Object.assign({ "x-router": router, dfid: params.dfid, mid: params.mid, clienttime: String(params.clienttime) }, KG_HEADERS),
    timeout: 10000,
  })).data;
  if (!res || res.status !== 1 || (res.error_code && res.error_code !== 0)) {
    throw new Error("酷狗请求失败 " + (res && (res.error_code || res.message) || ""));
  }
  return res.data || {};
}

function formatGatewaySong(it, fallbackArtwork) {
  const parts = splitCloudName(it.filename || it.name || it.songname || "");
  return {
    id: it.hash || it.filehash || "",
    title: it.songname || (it.author_name || it.singername ? it.name : parts[1]) || parts[1],
    artist: it.author_name || it.singername || parts[0],
    album: it.album_name || "",
    album_id: it.album_id || "",
    album_audio_id: it.mixsongid || it.album_audio_id || 0,
    duration: it.timelen ? Number(it.timelen) / 1000 : Number(it.time_length || it.duration || 0),
    artwork: String(it.sizable_cover || it.album_sizable_cover || it.cover || fallbackArtwork || "").replace(/\{size\}/g, "400"),
  };
}

async function getDailyRecommend() {
  const vars = env.getUserVariables();
  const userid = vars.userid || "0";
  const res = await gatewayPost("/everyday_song_recommend", { userid }, { platform: "android", userid }, {
    "x-router": "everydayrec.service.kugou.com",
  });
  if (!res || res.status !== 1 || (res.error_code && res.error_code !== 0)) {
    throw new Error("每日推荐获取失败");
  }
  return ((res.data && res.data.song_list) || []).map((item) => formatGatewaySong(item)).filter((item) => item.id);
}

// 每日听歌领 VIP（概念版活动）：上报一次听歌任务，领取一天 VIP。
// 每天首次播放时自动执行，失败不阻塞播放。
function localDateStr() {
  const n = new Date();
  const p = (v) => (v < 10 ? "0" : "") + v;
  return n.getFullYear() + "-" + p(n.getMonth() + 1) + "-" + p(n.getDate());
}

async function maybeClaimDailyVip(musicItem) {
  const vars = env.getUserVariables();
  if (!vars.token || !vars.userid) return "";
  const today = localDateStr();
  if (vars.vip_claim_date === today) return "already";
  try {
    const mixsongid = musicItem && musicItem.album_audio_id ? musicItem.album_audio_id : 666075191;
    await gatewayPost("/youth/v2/report/listen_song", { clientver: 10566 }, { mixsongid }, {
      "user-agent": "Android13-1070-10566-201-0-ReportPlaySongToServerProtocol-wifi",
      "content-type": "application/json; charset=utf-8",
    });
    const res = await gatewayPost("/youth/v1/recharge/receive_vip_listen_song",
      { source_id: 90139, receive_day: 1 }, null,
      { "content-type": "application/x-www-form-urlencoded" });
    vars.vip_claim_date = today;
    return "receive:" + JSON.stringify(res && (res.status !== undefined ? { status: res.status, error_code: res.error_code } : res)).slice(0, 200);
  } catch (e) {
    return "claim-error:" + String(e && e.message).slice(0, 150);
  }
}

// 高音质被付费墙拦截时（免费听时段结束等），自动降级到 128 保证能播
async function getMediaSource(musicItem, quality) {
  await maybeClaimDailyVip(musicItem);
  const wanted = QUALITIES[quality] || "128";
  const chain = wanted === "128" ? ["128"] : [wanted, "128"];
  for (const q of chain) {
    try {
      return await requestPlayUrl(musicItem, q);
    } catch (e) {
      // 只有音质被拒才降级重试；网络错误直接抛出
      if (String(e && e.message) !== "quality-unavailable") {
        throw e;
      }
    }
  }
  throw new Error("无法获取播放地址：该曲目当前不可播放（可能需要会员或版权受限）");
}

async function requestPlayUrl(musicItem, q) {
  const vars = env.getUserVariables();
  const userid = vars.userid || "0";

  const params = {
    album_id: musicItem.album_id || "",
    album_audio_id: musicItem.album_audio_id || 0,
    appid: APPID,
    behavior: "play",
    cdnBackup: 1,
    clienttime: clientTime(),
    clientver: CLIENTVER,
    cmd: 26,
    dfid: "-",
    hash: musicItem.id,
    mid: MID,
    need_ogg: 1,
    pid: 411,
    pidversion: 3001,
    quality: q,
    userid: userid,
    uuid: "-",
    version: 11436,
    volume: 0.55,
  };
  if (vars.token) {
    params.token = vars.token;
  }
  params.key = signKeyFor(musicItem.id, userid);
  params.signature = androidSignature(params);

  const res = (await axios.get("https://gateway.kugou.com/v5/url", {
    params,
    headers: Object.assign({
      "x-router": "trackercdn.kugou.com",
      dfid: params.dfid,
      mid: params.mid,
      clienttime: String(params.clienttime),
    }, KG_HEADERS),
    timeout: 15000,
  })).data;

  // v5/url 成功时把 url/backupUrl 直接放在响应根级，且可能是字符串或数组
  function firstUrl(v) {
    if (!v) return "";
    if (typeof v === "string") return v;
    if (Array.isArray(v) && v.length > 0) return String(v[0]);
    return "";
  }
  const url = firstUrl(res.url) || firstUrl(res.backupUrl) ||
    firstUrl(res.data && res.data.url) || firstUrl(res.data && res.data.backupUrl);
  if (!url) {
    throw new Error("quality-unavailable");
  }
  return { url, quality: q, headers: { "User-Agent": UA } };
}

// —— 搜索（公开端点，与竹玥版同源）——
function formatSearchItem(it) {
  const grp = it.Grp && it.Grp[0] ? it.Grp[0] : {};
  return {
    id: it.FileHash || grp.FileHash,
    title: it.SongName || it.OriSongName,
    artist: it.SingerName || (it.Singers && it.Singers[0] && it.Singers[0].name),
    album: it.AlbumName || grp.AlbumName,
    album_id: it.AlbumID || grp.AlbumID,
    album_audio_id: 0,
    duration: it.Duration,
    artwork: (it.Image || grp.Image || "").replace("{size}", "400"),
  };
}

function formatRankItem(it) {
  const artistFromName = it.filename ? it.filename.split("-")[0].trim() : "";
  return {
    id: it.hash,
    title: it.songname,
    artist: it.singername || artistFromName,
    album: it.album_name || it.remark,
    album_id: it.album_id,
    album_audio_id: it.album_audio_id,
    duration: it.duration,
    artwork: it.album_sizable_cover ? it.album_sizable_cover.replace("{size}", "400") : undefined,
  };
}

async function searchMusic(keyword, page, searchType) {
  if (searchType === "album") {
    return searchAlbum(keyword, page);
  }
  if (searchType === "sheet") {
    const data = await gatewayGet("/v1/search/special", {
      platform: "AndroidFilter", keyword, page, pagesize: PAGE_SIZE, category: 1,
    }, "complexsearch.kugou.com");
    const list = data.lists || data.list || data.info || [];
    return {
      isEnd: page * PAGE_SIZE >= Number(data.total || 0),
      data: list.map((it) => ({
        id: String(it.gid || ""), title: cheerio.load(it.specialname || "").text(),
        artwork: String(it.img || "").replace(/\{size\}/g, "400"),
        description: it.intro || "", count: it.song_count || 0,
      })).filter((it) => it.id && it.title),
    };
  }
  const res = (await axios.get("https://songsearch.kugou.com/song_search_v2", {
    headers: webHeaders,
    params: {
      keyword,
      page,
      pagesize: PAGE_SIZE,
      userid: 0,
      clientver: "",
      platform: "WebFilter",
      filter: 2,
      iscorrection: 1,
      privilege_filter: 0,
      area_code: 1,
    },
    timeout: 15000,
  })).data;
  return {
    isEnd: page * PAGE_SIZE >= res.data.total,
    data: (res.data.lists || []).map(formatSearchItem),
  };
}

async function getMusicSheetInfo(sheet, page) {
  page = page || 1;
  const data = await gatewayGet("/v4/get_other_list_file", {
    plat: 1, type: 1, module: "NONE", page, pagesize: 30, global_collection_id: sheet.id,
  }, "pubsongscdn.kugou.com");
  const list = data.list || data.info || [];
  return {
    isEnd: list.length < 30 || (Number(data.count) > 0 && page * 30 >= Number(data.count)),
    musicList: list.map((it) => formatGatewaySong(it, sheet.artwork)).filter((it) => it.id),
  };
}

async function searchAlbum(keyword, page) {
  const res = (await axios.get("http://msearch.kugou.com/api/v3/search/album", {
    headers: webHeaders,
    params: {
      version: 9108,
      iscorrection: 1,
      highlight: "em",
      plat: 0,
      keyword,
      pagesize: 20,
      page,
      sver: 2,
      with_res_tag: 0,
    },
    timeout: 15000,
  })).data;
  const albums = (res.data.info || []).map((it) => ({
    id: it.albumid,
    artwork: it.imgurl ? it.imgurl.replace("{size}", "400") : undefined,
    artist: it.singername,
    title: cheerio.load(it.albumname).text(),
    description: it.intro,
    date: it.publishtime ? it.publishtime.slice(0, 10) : undefined,
  }));
  return { isEnd: page * 20 >= res.data.total, data: albums };
}

async function getAlbumInfo(albumItem, page) {
  page = page || 1;
  const res = (await axios.get("http://mobilecdn.kugou.com/api/v3/album/song", {
    headers: webHeaders,
    params: {
      version: 9108,
      albumid: albumItem.id,
      plat: 0,
      pagesize: 100,
      area_code: 1,
      page,
      with_res_tag: 0,
    },
    timeout: 15000,
  })).data;
  return {
    isEnd: page * 100 >= res.data.total,
    musicList: (res.data.info || []).map((it) => {
      const parts = it.filename.split("-");
      return {
        id: it.hash,
        title: (parts[1] || it.filename).trim(),
        artist: parts[0].trim(),
        album: albumItem.title || albumItem.album,
        album_id: it.album_id,
        album_audio_id: it.album_audio_id,
        duration: it.duration,
        artwork: albumItem.artwork,
      };
    }),
  };
}

// —— 排行榜 ——
async function getTopLists() {
  const lists = (await axios.get("http://mobilecdnbj.kugou.com/api/v3/rank/list?version=9108&plat=0&showtype=2&parentid=0&apiver=6&area_code=1&withsong=0&with_res_tag=0", {
    headers: webHeaders,
    timeout: 15000,
  })).data.data.info;
  const groups = [
    { title: "热门榜单", data: [] },
    { title: "特色音乐榜", data: [] },
    { title: "全球榜", data: [] },
  ];
  const extra = { title: "其他", data: [] };
  lists.forEach((item) => {
    const entry = {
      id: item.rankid,
      description: item.intro,
      coverImg: item.imgurl ? item.imgurl.replace("{size}", "400") : undefined,
      title: item.rankname,
    };
    if (item.classify === 1 || item.classify === 2) groups[0].data.push(entry);
    else if (item.classify === 3 || item.classify === 5) groups[1].data.push(entry);
    else if (item.classify === 4) groups[2].data.push(entry);
    else extra.data.push(entry);
  });
  if (extra.data.length > 0) groups.push(extra);
  return groups;
}

async function getTopListDetail(topListItem) {
  const res = (await axios.get(`http://mobilecdnbj.kugou.com/api/v3/rank/song?version=9108&ranktype=0&plat=0&pagesize=100&area_code=1&page=1&volid=35050&rankid=${topListItem.id}&with_res_tag=0`, {
    headers: webHeaders,
    timeout: 15000,
  })).data;
  return Object.assign({}, topListItem, {
    musicList: (res.data.data.info || []).map(formatRankItem),
  });
}

// —— 歌词 ——
const lyricHeaders = {
  "KG-RC": 1,
  "KG-THash": "expand_search_manager.cpp:852736169:451",
  "User-Agent": "KuGou2012-9020-ExpandSearchManager",
};

async function getLyric(musicItem) {
  const search = (await axios.get("https://lyrics.kugou.com/search", {
    params: { ver: 1, man: "yes", client: "pc", keyword: musicItem.title, hash: musicItem.id, timelength: musicItem.duration || 0 },
    headers: lyricHeaders,
    timeout: 2500,
  })).data;
  const info = search.candidates && search.candidates[0];
  if (!info) return { rawLrc: "" };
  const download = async (fmt, timeout) => (await axios.get("https://lyrics.kugou.com/download", {
    params: { ver: 1, client: "pc", id: info.id, accesskey: info.accesskey, fmt, charset: "utf8" },
    headers: lyricHeaders,
    timeout,
  })).data;
  try {
    const krc = await download("krc", 2000);
    if (krc.content) return lyricDecoder.decodeKrc(krc.content);
  } catch (_) {
    // Missing, corrupt or unavailable KRC falls back to ordinary line lyrics.
  }
  const lrc = await download("lrc", 2500);
  return {
    rawLrc: lrc.content ? he.decode(CryptoJS.enc.Base64.parse(lrc.content).toString(CryptoJS.enc.Utf8)) : "",
  };
}

// —— 账号数据（只读同步）：个人信息 / 我的歌单 / 歌单歌曲 ——

function profileAvatar(info) {
  return String(info.pic || info.head || info.avatar || "").replace(/\{size\}/g, "240");
}

// 概念版联盟 VIP 状态（无需 RSA），失败不影响个人信息展示
async function fetchUnionVipLabel() {
  const vars = env.getUserVariables();
  const params = {
    appid: APPID,
    clientver: CLIENTVER,
    clienttime: clientTime(),
    dfid: "-",
    mid: MID,
    uuid: "-",
    busi_type: "concept",
  };
  if (vars.token) params.token = vars.token;
  if (vars.userid) params.userid = vars.userid;
  params.signature = androidSignature(params);
  try {
    const res = (await axios.get("https://kugouvip.kugou.com/v1/get_union_vip", {
      params,
      headers: Object.assign({}, KG_HEADERS),
      timeout: 3000,
    })).data;
    const d = (res && res.data) || {};
    // 概念版 VIP 状态在 busi_vip 数组的 concept 条目里（根级 vip_type 是主站 VIP）
    const concept = (Array.isArray(d.busi_vip) ? d.busi_vip : []).find((v) => v && v.busi_type === "concept");
    const active = !!(concept && concept.vip_end_time && concept.vip_end_time > "");
    if (!active) return "";
    const end = String(concept.vip_end_time);
    const day = end.replace(/\s.*$/, "").replace(/-/g, ".");
    return "概念版VIP 至 " + day;
  } catch (e) {
    return "";
  }
}

async function getUserProfile() {
  const vars = env.getUserVariables();
  if (!vars.token || !vars.userid) throw new Error("未登录");
  const clienttime = clientTime();
  const [res, vipLabel] = await Promise.all([gatewayPost("/v3/get_my_info", { plat: 1 }, {
    visit_time: clienttime,
    usertype: 1,
    p: liteRsaHex({ token: vars.token, clienttime: clienttime }),
    userid: Number(vars.userid),
  }, { "x-router": "usercenter.kugou.com" }), fetchUnionVipLabel()]);
  if (res && res.status !== 1 && res.error_code !== 0) {
    throw new Error("个人信息获取失败 " + (res.error_code !== undefined ? res.error_code : ""));
  }
  const info = (res && res.data) || {};
  return {
    nickname: info.nick_name || info.nickname || vars.nickname || "酷狗用户" + vars.userid,
    avatar: profileAvatar(info) || vars.avatar || "",
    vipLabel,
  };
}

function isFavoriteSheetName(name) {
  return /我喜欢/.test(name || "");
}

async function getUserSheets() {
  const vars = env.getUserVariables();
  if (!vars.token || !vars.userid) throw new Error("未登录");
  const res = await gatewayPost("/v7/get_all_list", { plat: 1 }, {
    userid: Number(vars.userid),
    token: vars.token,
    total_ver: 979,
    type: 2,
    page: 1,
    pagesize: 30,
  }, { "x-router": "cloudlist.service.kugou.com" });
  if (res && res.status !== 1 && res.error_code !== 0) {
    throw new Error("歌单获取失败 " + (res.error_code !== undefined ? res.error_code : ""));
  }
  const d = (res && res.data) || {};
  const raw = d.list || d.info || (Array.isArray(d) ? d : []);
  const arr = Array.isArray(raw) ? raw : (Array.isArray(raw && raw.list) ? raw.list : []);
  const sheets = arr.map((it) => ({
    id: String(it.listid || it.specialid || it.id || ""),
    title: it.name || it.title || "未命名歌单",
    coverImg: (it.pic || it.cover || it.imgurl || "").replace("{size}", "400"),
    count: Number(it.count || it.song_count || it.total || 0),
    isFavorite: isFavoriteSheetName(it.name || it.title),
  })).filter((s) => s.id);
  sheets.sort((a, b) => (b.isFavorite ? 1 : 0) - (a.isFavorite ? 1 : 0));
  return sheets;
}

// 云歌单的歌名常是「歌手 - 歌名.mp3」整串，拆成 artist/title
function splitCloudName(name) {
  let t = String(name || "").replace(/\.(mp3|flac|ogg|ape|m4a|wav|aac)$/i, "");
  const idx = t.indexOf(" - ");
  if (idx > 0) return [t.slice(0, idx).trim(), t.slice(idx + 3).trim()];
  return ["", t.trim()];
}

async function getSheetSongs(sheetId, page) {
  const vars = env.getUserVariables();
  if (!vars.token || !vars.userid) throw new Error("未登录");
  const res = await gatewayPost("/v4/get_list_all_file_v3", { plat: 1 }, {
    listid: String(sheetId),
    userid: Number(vars.userid),
    type: 0,
    page: page || 1,
    pagesize: 300,
    area_code: 1,
    allplatform: 1,
    show_cover: 1,
    token: vars.token,
  }, { "x-router": "cloudlist.service.kugou.com" });
  if (res && res.status !== 1 && res.error_code !== 0) {
    throw new Error("歌单歌曲获取失败 " + (res.error_code !== undefined ? res.error_code : ""));
  }
  const d = (res && res.data) || {};
  const arr = d.list || d.info || [];
  const items = arr.map((it) => {
    const parts = splitCloudName(it.singername ? (it.songname || it.name) : (it.filename || it.name || it.songname));
    return {
      id: it.hash || it.filehash || "",
      title: it.singername ? (it.songname || it.name || parts[1]) : parts[1],
      artist: it.singername || parts[0],
      album: it.album_name || "",
      album_id: it.album_id || "",
      album_audio_id: it.mixsongid || 0,
      duration: it.timelen ? Math.round(Number(it.timelen) / 1000) : (it.duration || 0),
      artwork: (it.album_sizable_cover || it.cover || "").replace("{size}", "400"),
    };
  }).filter((s) => s.id);
  return { isEnd: arr.length < 300, data: items };
}

module.exports = {
  platform: "酷狗概念版",
  author: "MusicGo",
  version: "1.1.0",
  description: "酷狗概念版音源：支持扫码登录，登录后可播放更高音质",
  supportedSearchType: ["music", "album", "sheet"],
  primaryKey: ["id"],
  cacheControl: "no-cache",
  hints: {
    builtin: true,
    logo: "https://is1-ssl.mzstatic.com/image/thumb/Purple221/v4/7d/8d/29/7d8d2948-1786-fcd2-d292-14b090a2b6b7/AppIconGlass-0-0-1x_U007epad-0-1-0-85-220.png/256x256bb.jpg",
  },
  userVariables: [
    { key: "token", name: "Token", hint: "扫码登录后自动写入" },
    { key: "userid", name: "用户 ID", hint: "扫码登录后自动写入" },
  ],
  search: searchMusic,
  getMediaSource,
  getLyric,
  getTopLists,
  getTopListDetail,
  getAlbumInfo,
  getMusicSheetInfo,
  getDailyRecommend,
  getLoginQR,
  pollLoginQR,
  getLoginStatus,
  getUserProfile,
  getUserSheets,
  getSheetSongs,
};
