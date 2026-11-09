// QQ 音乐内置插件：通过 QQ 音乐接口获取搜索结果、歌词和账号有权播放的音源。
const axios = require("axios");
const CryptoJS = require("crypto-js");
const he = require("he");

const PAGE_SIZE = 20;
const API = "https://u.y.qq.com/cgi-bin/musicu.fcg";
const WX_APPID = "wx48db31d50e334801";

function credentials() {
  const vars = env.getUserVariables();
  return { uin: vars.musicid || "0", key: vars.musickey || "" };
}

function headers() {
  const { uin, key } = credentials();
  const result = { Referer: "https://y.qq.com/", Origin: "https://y.qq.com" };
  if (key && uin !== "0") {
    result.Cookie = `uin=${uin}; wxuin=${uin}; qm_keyst=${key}; qqmusic_key=${key}; tmeLoginType=1; login_type=2`;
  }
  return result;
}

function comm() {
  const { uin, key } = credentials();
  const value = { uin, format: "json", ct: key ? 19 : 24, cv: 0 };
  if (key) Object.assign(value, { authst: key, uid: uin, qq: uin, tmeLoginType: "1" });
  return value;
}

async function request(module, method, param, options) {
  const name = options && options.search ? module : "req";
  const body = { [name]: { module, method, param } };
  if (!(options && options.search)) body.comm = comm();
  const result = (await axios.post(API, body, { headers: headers(), timeout: (options && options.timeout) || 12000 })).data;
  const block = result && result[name];
  if (!result || Number(result.code || 0) !== 0 || !block || Number(block.code || 0) !== 0) {
    throw new Error("QQ 音乐接口请求失败：" + ((block && (block.code || block.message)) || (result && (result.code || result.message)) || "响应无效"));
  }
  return block.data || {};
}

function text(value) {
  return he.decode(String(value || "").replace(/<[^>]*>/g, ""));
}

function albumCover(mid) {
  return mid ? `https://y.gtimg.cn/music/photo_new/T002R300x300M000${mid}.jpg` : "";
}

function song(item) {
  item = item.songInfo || item.track || item;
  const album = item.album || {};
  const mid = item.mid || item.songmid || "";
  return {
    id: String(mid), songmid: String(mid), songid: item.id || item.songid || 0,
    mediaMid: (item.file && (item.file.media_mid || item.file.mediaMid)) || mid,
    title: text(item.title || item.name || item.songname),
    artist: (item.singer || []).map((s) => text(s.name || s.title)).join(", "),
    album: text(album.title || album.name || item.albumname),
    duration: Number(item.interval || item.duration || 0),
    artwork: albumCover(album.mid || item.albummid),
  };
}

async function search(keyword, page, type) {
  const types = { music: [0, "song"], artist: [1, "singer"], album: [2, "album"], sheet: [3, "songlist"] };
  const spec = types[type];
  if (!spec) return { isEnd: true, data: [] };
  const data = await request("music.search.SearchCgiService", "DoSearchForQQMusicDesktop", {
    query: keyword, search_type: spec[0], page_num: page || 1, num_per_page: PAGE_SIZE,
  }, { search: true });
  const group = data.body && data.body[spec[1]];
  if (!group || !Array.isArray(group.list)) throw new Error("QQ 音乐搜索响应无效");
  const items = group.list.map((it) => {
    if (type === "music") return song(it);
    if (type === "artist") {
      const mid = it.singerMID || it.singerMid || it.singermid || "";
      return { id: String(mid), title: text(it.singerName || it.name), artwork: it.singerPic || (mid ? `https://y.gtimg.cn/music/photo_new/T001R300x300M000${mid}.jpg` : "") };
    }
    if (type === "album") {
      const mid = it.albumMID || it.albumMid || it.album_mid || "";
      return { id: String(mid), title: text(it.albumName || it.album_name), artist: text(it.singerName || it.singer_name), artwork: it.albumPic || albumCover(mid) };
    }
    return { id: String(it.dissid || it.diss_id || ""), title: text(it.dissname || it.diss_name), artist: text((it.creator || {}).name), artwork: it.imgurl || it.cover || "" };
  }).filter((it) => it.id && it.title);
  const total = Number((data.meta || {}).sum || group.total || 0);
  return { isEnd: total ? page * PAGE_SIZE >= total : group.list.length < PAGE_SIZE, data: items };
}

function pageResult(data, list, page) {
  const total = Number(data.totalNum || data.total_song_num || data.total || 0);
  const end = data.hasmore !== undefined ? !(data.hasmore === true || Number(data.hasmore) > 0) : (total ? page * PAGE_SIZE >= total : list.length < PAGE_SIZE);
  return { isEnd: end, musicList: list.map(song).filter((it) => it.id && it.title) };
}

async function getAlbumInfo(item, page) {
  page = page || 1;
  const data = await request("music.musichallAlbum.AlbumSongList", "GetAlbumSongList", {
    albumMid: item.id, begin: (page - 1) * PAGE_SIZE, num: PAGE_SIZE, order: 2,
  });
  if (!Array.isArray(data.songList)) throw new Error("QQ 音乐专辑响应无效");
  return pageResult(data, data.songList, page);
}

async function getArtistWorks(item, page) {
  page = page || 1;
  const data = await request("musichall.song_list_server", "GetSingerSongList", {
    singerMid: item.id, order: 1, begin: (page - 1) * PAGE_SIZE, num: PAGE_SIZE,
  });
  if (!Array.isArray(data.songList)) throw new Error("QQ 音乐歌手响应无效");
  return pageResult(data, data.songList, page);
}

async function getMusicSheetInfo(item, page) {
  page = page || 1;
  const id = Number(item.id);
  if (!Number.isSafeInteger(id) || id <= 0) throw new Error("无效的 QQ 音乐歌单 ID");
  const data = await request("music.srfDissInfo.DissInfo", "CgiGetDiss", {
    disstid: id, dirid: 0, tag: true, song_begin: (page - 1) * PAGE_SIZE,
    song_num: PAGE_SIZE, userinfo: true, orderlist: true, onlysonglist: false,
  });
  if (!Array.isArray(data.songlist)) throw new Error("QQ 音乐歌单响应无效");
  return pageResult(data, data.songlist, page);
}

async function getMediaSource(item, quality) {
  const mid = String(item.songmid || item.id || "");
  if (!mid) throw new Error("缺少 QQ 音乐歌曲 ID");
  const variants = [
    { prefix: "RS01", ext: ".flac", quality: "hires" },
    { prefix: "F000", ext: ".flac", quality: "flac" },
    { prefix: "M800", ext: ".mp3", quality: "320" },
    { prefix: "M500", ext: ".mp3", quality: "128" },
    { prefix: "C400", ext: ".m4a", quality: "aac" },
  ];
  const start = { super: 0, high: 1, lossless: 1, standard: 2, low: 3 }[quality];
  const selected = variants.slice(start === undefined ? 2 : start);
  const mediaIDs = [String(item.mediaMid || mid)];
  if (mediaIDs[0] !== mid) mediaIDs.push(mid);
  const candidates = [];
  for (const variant of selected) {
    for (const mediaID of mediaIDs) {
      candidates.push({ ...variant, filename: variant.prefix + mediaID + variant.ext });
      candidates.push({ ...variant, filename: variant.prefix + mid + mediaID + variant.ext });
    }
  }
  const guid = String(Math.floor(Math.random() * 90000000) + 10000000);
  const { uin } = credentials();
  const data = await request("vkey.GetVkeyServer", "CgiGetVkey", {
    guid, uin, loginflag: 1, platform: "20", filename: candidates.map((v) => v.filename),
    songmid: candidates.map(() => mid), songtype: candidates.map(() => 0),
  });
  const infos = Array.isArray(data.midurlinfo) ? data.midurlinfo : [];
  for (let i = 0; i < candidates.length; i++) {
    const candidate = candidates[i];
    const info = infos.find((v) => v.filename === candidate.filename && v.purl) || (infos[i] && !infos[i].filename && infos[i].purl ? infos[i] : null);
    if (!info) continue;
    const hosts = Array.isArray(data.sip) ? data.sip : [];
    const host = hosts.find((s) => /^https:\/\//i.test(s)) || hosts[0] || "https://ws.stream.qqmusic.qq.com/";
    return { url: /^https?:\/\//i.test(info.purl) ? info.purl : host + info.purl, quality: candidate.quality, headers: { Referer: "https://y.qq.com/" } };
  }
  throw new Error("QQ 音乐未返回可播放音源，可能需要登录、会员或受版权限制");
}

function decodeLyric(value) {
  if (!value) return "";
  const raw = String(value);
  if (/\[\d{1,3}:\d{2}/.test(raw)) return he.decode(raw);
  try { return he.decode(CryptoJS.enc.Base64.parse(raw).toString(CryptoJS.enc.Utf8)); } catch (_) { return ""; }
}

async function getLyric(item) {
  const mid = item.songmid || item.id;
  try {
    const data = await request("music.musichallSong.PlayLyricInfo", "GetPlayLyricInfo", { songMID: mid }, { timeout: 4000 });
    const rawLrc = decodeLyric(data.lyric);
    if (/\[\d{1,3}:\d{2}/.test(rawLrc)) return { rawLrc, translation: decodeLyric(data.trans) };
  } catch (_) {
    // The legacy lyric endpoint can still provide line lyrics.
  }
  let data = (await axios.get("https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg", {
    params: { songmid: mid, format: "json", nobase64: 1, g_tk: 5381, platform: "yqq.json" },
    headers: headers(), timeout: 3000,
  })).data;
  if (typeof data === "string") data = JSON.parse(data.replace(/^\s*\w+\(([\s\S]*)\)\s*;?\s*$/, "$1"));
  return { rawLrc: decodeLyric(data.lyric), translation: decodeLyric(data.trans) };
}

async function getLoginQR() {
  const response = await axios.get("https://open.weixin.qq.com/connect/qrconnect", {
    params: { appid: WX_APPID, redirect_uri: "https://y.qq.com/portal/wx_redirect.html?login_type=2&surl=https://y.qq.com/", response_type: "code", scope: "snsapi_login", state: "STATE" },
    timeout: 12000,
  });
  const match = String(response.data).match(/uuid=([^"&\s]+)/);
  if (!match) throw new Error("微信登录未返回二维码");
  return { qrImageUrl: "https://open.weixin.qq.com/connect/qrcode/" + encodeURIComponent(match[1]), state: match[1], message: "使用微信扫码登录 QQ 音乐" };
}

async function pollLoginQR(uuid) {
  let response;
  try {
    response = await axios.get("https://lp.open.weixin.qq.com/connect/l/qrconnect", { params: { uuid, _: Date.now() }, headers: { Referer: "https://open.weixin.qq.com/" }, timeout: 3000 });
  } catch (e) {
    if (/timeout|deadline exceeded/i.test(String(e && e.message))) return { status: "waiting" };
    throw e;
  }
  const raw = String(response.data);
  const status = (raw.match(/wx_errcode\s*=\s*(\d+)/) || [])[1];
  if (status === "404") return { status: "scanned" };
  if (status === "402" || status === "403") return { status: "expired" };
  if (status !== "405") return { status: "waiting" };
  const code = (raw.match(/wx_code\s*=\s*['"]([^'"]+)/) || [])[1];
  if (!code) throw new Error("微信登录未返回授权码");
  const result = (await axios.post(API, {
    comm: { ct: 24, cv: 0, tmeLoginType: "1" },
    req: { module: "music.login.LoginServer", method: "Login", param: { code, strAppid: WX_APPID } },
  }, { headers: headers(), timeout: 12000 })).data;
  const block = result && result.req;
  const user = block && block.data;
  const musicid = user && (user.str_musicid || user.musicid || user.uin);
  const musickey = user && (user.musickey || user.music_key);
  if (!result || Number(result.code || 0) !== 0 || !block || Number(block.code || 0) !== 0 || !musicid || !musickey) throw new Error("QQ 音乐登录未返回完整凭据");
  return { status: "confirmed", userVariables: { musicid: String(musicid), musickey: String(musickey), nickname: String(user.nick || user.nickname || "QQ 音乐用户") } };
}

function getLoginStatus() {
  const vars = env.getUserVariables();
  return { loggedIn: !!(vars.musicid && vars.musickey), message: vars.nickname || "已登录" };
}

module.exports = {
  platform: "QQ音乐", author: "MusicGo", version: "1.0.0", description: "QQ 音乐：支持微信扫码登录、搜索、歌词和播放",
  supportedSearchType: ["music", "album", "sheet", "artist"], primaryKey: ["id"], cacheControl: "no-cache", hints: { builtin: true, logo: "https://is1-ssl.mzstatic.com/image/thumb/Purple221/v4/d9/b5/eb/d9b5eb6d-4390-1868-bf01-d8a959668cb4/AppIcon-0-0-1x_U007epad-0-1-0-sRGB-85-220.png/512x512bb.jpg" },
  search, getMediaSource, getLyric, getAlbumInfo, getMusicSheetInfo, getArtistWorks, getLoginQR, pollLoginQR, getLoginStatus,
};
