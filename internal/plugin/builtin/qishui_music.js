// 汽水音乐内置插件：使用公开搜索、歌词和 H5 曲目接口。
const axios = require("axios");
const CryptoJS = require("crypto-js");
const qs = require("qs");

const PAGE_SIZE = 20;
const VEHICLE_API = "https://api-vehicle.volcengine.com";
const H5_API = "https://api.qishui.com/luna/h5/track_v2";
const SEO_API = "https://beta-luna.douyin.com/luna/h5/seo_track";
const H5_PARAMS = { aid: 386088, app_name: "luna_pc", device_platform: "windows", channel: "official", version_code: 30070000 };
const ACCOUNT_API = "https://api.qishui.com";
const ACCOUNT_UA = "LunaPC/3.0.0(290101097)";
let loginAttempt = null;
let loginSequence = 0;
let publicDevice = null;
const publicSearches = new Map();
const artistCursors = {};
const sheetCursors = {};

// 汽水 ID 可能超过 JS 安全整数范围，解析前保留整数原文。
function jsonData(data) {
  if (typeof data !== "string") return data;
  return JSON.parse(data.replace(/"(?:\\.|[^"\\])*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/g,
    (token) => /^-?\d{16,}$/.test(token) ? `"${token}"` : token));
}

function itemID(value) {
  if (typeof value === "number" && !Number.isSafeInteger(value)) return "";
  return String(value || "");
}

async function search(keyword, page, type) {
  page = page || 1;
  if (type !== "music") return searchCatalog(keyword, page, type);
  const response = (await axios.get(VEHICLE_API + "/v2/search/type", {
    params: { keyword, search_type: "music", limit: PAGE_SIZE, real_offset: (page - 1) * PAGE_SIZE, search_source: "qishui" },
    timeout: 12000, responseType: "text", maxContentLength: 2097152,
  })).data;
  const data = jsonData(response).data;
  if (!data || !Array.isArray(data.list)) throw new Error("汽水音乐搜索响应无效");
  return {
    isEnd: data.list.length < PAGE_SIZE,
    data: data.list.map((it) => ({
      id: itemID(it.item_id), title: String(it.title || ""),
      artist: String((it.author_info || {}).name || ""),
      album: String((it.album_info || {}).name || ""),
      // Vehicle search returns seconds; the PC/H5 track APIs return milliseconds.
      duration: Number(it.duration || 0), artwork: it.cover_url || "",
    })).filter((it) => it.id && it.title),
  };
}

function trackItem(track) {
  const album = track.album || {};
  return {
    id: itemID(track.id), title: String(track.name || ""),
    artist: (track.artists || []).map((a) => a.name || "").join(", "),
    album: String(album.name || ""), duration: Number(track.duration || 0) / 1000,
    artwork: accountImage(album.cover_url || album.url_cover),
  };
}

function resourceSongs(resources) {
  const songs = [];
  for (const resource of resources) {
    if (!resource || typeof resource !== "object") continue;
    const entity = resource.entity || {};
    const track = entity.track || (entity.track_wrapper && entity.track_wrapper.track);
    if (!track) continue;
    const item = trackItem(track);
    if (item.id && item.title) songs.push(item);
  }
  return songs;
}

function publicParams() {
  if (!publicDevice) publicDevice = loginIdentity();
  return pcParams(publicDevice.device, publicDevice.install);
}

function pcParams(device, install) {
  return {
    aid: 386088, app_name: "luna_pc", device_platform: "windows", device_type: "Windows", channel: "official",
    device_id: device, iid: install, fp: device, cdid: "", os_version: "Windows 11", ac: "wifi", tz_name: "Asia/Shanghai",
    version_name: "3.7.0", version_code: 30070000, region: "cn", geo_region: "cn", os_region: "cn", build_mode: "master",
  };
}

async function publicGet(path, params) {
  const response = await axios.get(ACCOUNT_API + path, {
    params: Object.assign(publicParams(), params), headers: { "User-Agent": "LunaPC/3.7.0(452316191)", Accept: "application/json" },
    maxRedirects: 0, responseType: "text", timeout: 8000, maxContentLength: 2097152,
  });
  if (response.status !== 200) throw new Error("汽水公开目录请求未完成");
  return checkedResponse(jsonData(response.data));
}

async function searchCatalog(keyword, page, type) {
  if (!["artist", "album", "sheet"].includes(type)) return { isEnd: true, data: [] };
  const kind = type === "sheet" ? "playlist" : type;
  const plural = { artist: "artists", album: "albums", playlist: "playlists" }[kind];
  const key = kind + "\n" + keyword;
  if (page === 1) {
    if (publicSearches.size >= 16) publicSearches.delete(publicSearches.keys().next().value);
    publicSearches.set(key, { id: randomUUID(), cursors: { 1: "0" } });
  }
  const search = publicSearches.get(key);
  if (!search || search.cursors[page] === undefined) throw new Error("汽水搜索分页状态已失效");
  const data = await publicGet("/luna/pc/search/" + kind, {
    q: keyword, cursor: search.cursors[page], search_id: search.id, search_method: "input",
    debug_params: "", from_search_id: "", search_scene: "",
  });
  if (!Array.isArray(data.result_groups)) throw new Error("汽水搜索响应无效");
  const group = data.result_groups.find((g) => g.id === plural);
  if (!group) return { isEnd: true, data: [] };
  if (group.data != null && !Array.isArray(group.data)) throw new Error("汽水搜索结果无效");
  const items = (group.data || []).map((row) => {
    if (!row || typeof row !== "object") return null;
    const raw = row.entity && row.entity[kind];
    if (!raw) return null;
    const id = itemID(raw.id);
    if (!id) return null;
    if (kind === "artist") return { id, title: String(raw.name || ""), artwork: accountImage(raw.url_avatar) };
    if (kind === "album") return { id, title: String(raw.name || ""), artist: (raw.artists || []).map((a) => a.name || "").join(", "), artwork: accountImage(raw.url_cover) };
    return { id, title: String(raw.title || raw.public_title || ""), artist: String((raw.owner || {}).nickname || ""), artwork: accountImage(raw.cover_url || raw.url_cover) };
  }).filter((it) => it && it.title);
  if (group.has_more) {
    const next = itemID(group.next_cursor);
    if (!next || next === search.cursors[page]) throw new Error("汽水搜索游标无效");
    search.cursors[page + 1] = next;
  }
  return { isEnd: !group.has_more, data: items };
}

async function getArtistWorks(item, page) {
  if (!/^\d{1,32}$/.test(String(item.id))) throw new Error("汽水歌手 ID 无效");
  page = page || 1;
  if (page === 1) artistCursors[item.id] = { 1: "" };
  const cursors = artistCursors[item.id];
  if (!cursors || cursors[page] === undefined) throw new Error("汽水歌手分页状态已失效");
  const data = await publicGet(`/luna/pc/artists/${item.id}/tracks`, { cursor: cursors[page], count: 50, sort_type: 0 });
  if (data.tracks == null && !data.has_more && data.status_info) data.tracks = [];
  if (!Array.isArray(data.tracks)) throw new Error("汽水歌手作品响应无效");
  if (data.has_more) {
    const next = itemID(data.next_cursor);
    if (!next || next === cursors[page]) throw new Error("汽水歌手分页游标无效");
    cursors[page + 1] = next;
  }
  return { isEnd: !data.has_more, musicList: data.tracks.map(trackItem).filter((track) => track.id && track.title) };
}

async function getAlbumInfo(item, page) {
  if (!/^\d{1,32}$/.test(String(item.id))) throw new Error("汽水专辑 ID 无效");
  if (page > 1) return { isEnd: true, musicList: [] };
  const data = await publicGet(`/luna/pc/albums/${item.id}`, { ignore_tracks: false });
  if (data.tracks == null && data.album_info) data.tracks = [];
  if (!Array.isArray(data.tracks)) throw new Error("汽水专辑曲目响应无效");
  return { isEnd: true, musicList: data.tracks.map(trackItem).filter((track) => track.id && track.title) };
}

async function getMusicSheetInfo(item, page) {
  if (!/^\d{1,32}$/.test(String(item.id))) throw new Error("汽水歌单 ID 无效");
  if (env.getUserVariables().cookie) return getSheetSongs(item.id, page);
  if (page > 1) return { isEnd: true, musicList: [] };
  const response = await axios.post("https://api5-lq.qishui.com/luna/playlist/detail?charge=0", { playlist_id: String(item.id) }, {
    headers: { "User-Agent": "com.luna.music/100159040 (Linux; U; Android 11; zh_CN)", "x-common-params-v2": "channel=appstore&aid=8478&device_id=1100210274091033" },
    responseType: "text", timeout: 8000, maxContentLength: 2097152,
  });
  const data = checkedResponse(jsonData(response.data));
  if (!Array.isArray(data.media_resources)) throw new Error("汽水歌单内容不可用，请尝试登录后重试");
  if (data.has_more) throw new Error("汽水公开歌单只返回部分曲目，请登录后重试");
  return { isEnd: true, musicList: resourceSongs(data.media_resources) };
}

async function getTrack(id, seo) {
  const data = jsonData((await axios.get(seo ? SEO_API : H5_API, {
    params: seo ? { track_id: id, device_platform: "web" } : Object.assign({}, H5_PARAMS, { track_id: id, media_type: "track" }),
    timeout: 4000, responseType: "text", maxContentLength: 2097152,
  })).data);
  const track = data && (data.track || (data.seo_track && data.seo_track.track));
  if (!track || Number(data.status_code || 0) !== 0 || itemID(track.id) !== String(id)) throw new Error("汽水音乐曲目信息不可用");
  return { track, track_player: data.track_player, lyric: data.lyric || (data.seo_track && data.seo_track.lyric) };
}

async function getMediaSource(item) {
  if (!item.id) throw new Error("缺少汽水音乐歌曲 ID");
  for (const seo of [true, false]) {
    try {
      const track = await getTrack(item.id, seo);
      const playerInfo = track.track_player && track.track_player.url_player_info;
      const fullDuration = Number(track.track.duration || 0) / 1000 || Number(item.duration || 0);
      if (!playerInfo || !/^https:\/\//i.test(playerInfo) || fullDuration <= 0) continue;
      const response = (await axios.get(playerInfo, { timeout: 4000, maxContentLength: 2097152 })).data;
      const list = response && response.Result && response.Result.Data && response.Result.Data.PlayInfoList;
      if (!Array.isArray(list)) continue;
      for (const audio of list) {
        const duration = Number(audio.Duration || 0);
        const url = audio.MainPlayUrl || audio.BackupPlayUrl || "";
        if (/^https:\/\//i.test(url) && duration > 0 && duration + 5 >= fullDuration && !audio.EncryptionMethod && !audio.PlayAuth) return { url };
      }
    } catch (_) {
      // An unavailable public endpoint must not prevent trying the other one.
    }
  }
  throw new Error("汽水音乐未提供可确认的完整未加密音源");
}

// H5 歌词为毫秒行时间时，只转换可核实的行时间，不推测逐字时间。
function lineLyric(value) {
  return String(value || "").split("\n").map((line) => {
    const match = line.match(/^\[(\d+),\d+\](.*)$/);
    if (!match) return line;
    const ms = Number(match[1]);
    const min = Math.floor(ms / 60000);
    const sec = Math.floor(ms % 60000 / 1000);
    const fraction = ms % 1000;
    return `[${min}:${String(sec).padStart(2, "0")}.${String(fraction).padStart(3, "0")}]` + match[2].replace(/<\d+,\d+(?:,\d+)?>/g, "");
  }).join("\n");
}

async function getLyric(item) {
  try {
    const response = (await axios.get(VEHICLE_API + "/v2/custom/contents", {
      params: { sources: "qishui", need_author: true, need_album: true, need_ugc: true, need_stat: true, item_ids: item.id },
      timeout: 3000, responseType: "text", maxContentLength: 2097152,
    })).data;
    const data = jsonData(response);
    const first = data && data.data && data.data.list && data.data.list[0];
    const lyric = first && first.lyric_info && first.lyric_info.lyric_text;
    if (lyric) return { rawLrc: lineLyric(lyric) };
  } catch (_) {
    // Fall back to the H5 track metadata when the vehicle endpoint is unavailable.
  }
  const track = await getTrack(item.id, true);
  return { rawLrc: lineLyric(track.lyric && track.lyric.content) };
}

function randomChars(alphabet, count) {
  let value = "";
  while (value.length < count) value += alphabet[Math.floor(Math.random() * alphabet.length)];
  return value;
}

function randomUUID() {
  const hex = "0123456789abcdef";
  return [randomChars(hex, 8), randomChars(hex, 4), "4" + randomChars(hex, 3), randomChars("89ab", 1) + randomChars(hex, 3), randomChars(hex, 12)].join("-");
}

function loginIdentity() {
  const digits = "0123456789";
  return {
    device: String(1 + Math.floor(Math.random() * 9)) + randomChars(digits, 15),
    install: String(1 + Math.floor(Math.random() * 9)) + randomChars(digits, 14),
    portrait: randomUUID() + ".login",
    token: randomChars("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", 118) + "==",
  };
}

function allowedLoginURL(value) {
  return typeof value === "string" && value.length <= 16384 && !/[\s\\]/.test(value) &&
    /^https:\/\/(?:[a-z0-9-]+\.)*qishui\.com(?::443)?(?:[/?#]|$)/i.test(value);
}

async function loginRequest(client, address, options, expectJSON = true) {
  let current = address;
  let config = options || {};
  for (let hop = 0; hop < 6; hop++) {
    if (!allowedLoginURL(current)) throw new Error("汽水登录返回了无效地址");
    const response = await client(Object.assign({ url: current, method: "GET" }, config));
    if (response.status < 300) return expectJSON ? jsonData(response.data) : response.data;
    const location = response.headers && response.headers.location;
    if (!location) throw new Error("汽水登录跳转地址缺失");
    if (location[0] === "/" && location[1] !== "/") {
      current = (current.match(/^https:\/\/[^/]+/i) || [""])[0] + location;
    } else {
      current = location;
    }
    if (response.status !== 307 && response.status !== 308) {
      config = {};
    } else {
      config = Object.assign({}, config);
      delete config.params;
    }
  }
  throw new Error("汽水登录跳转次数过多");
}

function checkedResponse(data) {
  if (!data || typeof data !== "object") throw new Error("汽水登录响应无效");
  const codes = [data.error_code, data.status_code, data.status_info && data.status_info.code, data.data && data.data.error_code];
  for (const code of codes) {
    if (!code || Number(code) === 0) continue;
    if (Number(code) === 2046) throw new Error("汽水要求额外安全验证，请在官方 App 完成后重试");
    if (Number(code) === 7) throw new Error("汽水暂时限制登录请求，请稍后重试");
    throw new Error("汽水登录失败，错误码 " + String(code));
  }
  return data;
}

async function passport(attempt, path, form, extra) {
  const id = attempt.identity;
  const trace = randomChars("0123456789abcdef", 8);
  const params = Object.assign({
    aid: "386088", passport_jssdk_version: "2.4.13", passport_jssdk_type: "normal",
    is_from_ttaccountsdk: "1", language: "zh", account_sdk_source: "web",
    p_js_v: "2.4.13", p_js_t: "pro", p_zt: "3.3.5", p_ver: "1.0.29",
    request_host: "app%3A%2F%2Fresources", p_bd: "1.0.0.41", is_new_login: "1",
    is_from_iesaccountsaas: "1", device_platform: "PC", version_code: "3.5.2", account_sdk_source_info: "00",
    biz_trace_id: trace, device_id: id.device, did: id.device, install_id: id.install, iid: id.install, msToken: id.token,
  }, extra || {});
  const headers = { "User-Agent": ACCOUNT_UA, Accept: "application/json, text/javascript", "x-tt-passport-verify-portrait": id.portrait, "x-tt-passport-trace-id": trace };
  const options = { params, headers };
  if (form) {
    const body = qs.stringify(form);
    options.method = "POST";
    options.data = body;
    headers["Content-Type"] = "application/x-www-form-urlencoded";
    headers["x-ss-stub"] = CryptoJS.MD5(body).toString().toUpperCase();
  }
  return checkedResponse(await loginRequest(attempt.client, ACCOUNT_API + path, options));
}

function accountImage(value) {
  if (typeof value === "string") return /^https:\/\//i.test(value) ? value : "";
  if (!value) return "";
  if (value.uri && value.template_prefix) return `https://p3-luna.douyinpic.com/img/${value.uri}~${value.template_prefix}-resize:300:300.png`;
  return Array.isArray(value.urls) ? (value.urls.find((url) => /^https:\/\//i.test(url)) || "") : "";
}

function profileFromResponse(data) {
  checkedResponse(data);
  const user = data.my_info || data.user || (data.data && data.data.my_info);
  const userid = user && itemID(user.id);
  if (!userid || !/^\d+$/.test(userid)) throw new Error("汽水没有返回有效账号信息");
  return {
    userid, nickname: String(user.nickname || user.public_name || "汽水音乐用户"),
    avatar: accountImage(user.avatar_url || user.larger_avatar_url || user.medium_avatar_url || user.url_avatar),
    vip: user.is_vip === true ? (user.vip_stage === "svip" ? "SVIP" : "VIP") : "",
  };
}

async function getLoginQR() {
  const attempt = { id: String(++loginSequence), identity: loginIdentity(), client: axios.create({ withCredentials: true, maxRedirects: 0, responseType: "text", timeout: 5000, maxContentLength: 1048576 }) };
  loginAttempt = attempt;
  const response = await passport(attempt, "/passport/web/get_qrcode/", null, { next: ACCOUNT_API, need_logo: "false", need_short_url: "false" });
  const qr = response.data;
  if (!qr || typeof qr.token !== "string" || !qr.token || !allowedLoginURL(qr.qrcode_index_url)) throw new Error("汽水没有返回有效二维码");
  attempt.token = qr.token;
  attempt.frontier = qr.is_frontier === true;
  return { qrText: qr.qrcode_index_url, state: attempt.id, message: "打开汽水音乐 App 扫码登录" };
}

async function pollLoginQR(state) {
  const attempt = loginAttempt;
  if (!attempt || attempt.id !== state) return { status: "expired" };
  const response = await passport(attempt, "/passport/web/check_qrconnect/", {
    token: attempt.token, next: ACCOUNT_API, need_logo: "false", need_short_url: "false",
    is_frontier: String(attempt.frontier), is_new_login: "1",
  });
  const data = response.data || {};
  const status = String(data.status || "").toLowerCase();
  if (status === "new" || status === "1") return { status: "waiting" };
  if (status === "scanned" || status === "2") return { status: "scanned" };
  if (["expired", "expire", "timeout", "cancelled", "canceled", "rejected", "denied"].includes(status)) return { status: "expired" };
  if (!["confirmed", "success", "3"].includes(status)) throw new Error("汽水登录状态不可识别");
  const sessionCookie = () => attempt.client.getCookieHeader(ACCOUNT_API + "/");
  const hasSession = () => /(?:^|;\s*)(?:sessionid|sessionid_ss|sid_guard|sid_tt)=[^;]+/i.test(sessionCookie());
  if (!hasSession() && data.redirect_url) await loginRequest(attempt.client, data.redirect_url, null, false);
  if (!hasSession()) throw new Error("汽水没有返回有效登录会话");
  const profile = profileFromResponse(await loginRequest(attempt.client, ACCOUNT_API + "/luna/pc/me?aid=386088", { headers: { "User-Agent": ACCOUNT_UA } }));
  if (loginAttempt !== attempt) return { status: "expired" };
  loginAttempt = null;
  return { status: "confirmed", userVariables: Object.assign(profile, { cookie: sessionCookie(), device_id: attempt.identity.device, install_id: attempt.identity.install }) };
}

function getLoginStatus() {
  const vars = env.getUserVariables();
  return { loggedIn: !!(vars.cookie && vars.userid), message: vars.nickname || "已登录" };
}

async function accountGet(path, params) {
  const vars = env.getUserVariables();
  if (!vars.cookie || !vars.userid) throw new Error("请先登录汽水音乐");
  const response = await axios.get(ACCOUNT_API + path, {
    params, headers: { Cookie: vars.cookie, "User-Agent": ACCOUNT_UA },
    maxRedirects: 0, responseType: "text", timeout: 8000, maxContentLength: 2097152,
  });
  if (response.status !== 200) throw new Error("汽水账号请求未完成");
  return checkedResponse(jsonData(response.data));
}

async function getUserProfile() {
  const p = profileFromResponse(await accountGet("/luna/pc/me", { aid: 386088 }));
  if (p.userid !== env.getUserVariables().userid) throw new Error("汽水账号已变化，请重新登录");
  return { nickname: p.nickname, avatar: p.avatar, vipLabel: p.vip };
}

async function getUserSheets() {
  const result = [];
  const seen = {};
  const seenCursors = new Set([""]);
  let cursor = "";
  for (let page = 0; page < 50; page++) {
    const data = await accountGet("/luna/pc/me/playlist", { aid: 386088, version_code: 30020100, cursor, count: 50 });
    if (!Array.isArray(data.playlists)) throw new Error("汽水歌单响应无效");
    for (const list of data.playlists) {
      const id = itemID(list.id);
      if (id && !seen[id]) {
        seen[id] = true;
        result.push({ id, title: String(list.title || list.public_title || "歌单"), coverImg: accountImage(list.cover_url || list.url_cover), count: Number(list.count_tracks || 0) });
      }
    }
    if (!data.has_more) return result;
    const next = itemID(data.next_cursor);
    if (!next || seenCursors.has(next)) throw new Error("汽水歌单分页游标无效");
    cursor = next;
    seenCursors.add(cursor);
  }
  throw new Error("汽水歌单数量超过读取上限");
}

async function getSheetSongs(sheet, page) {
  if (!/^\d{1,32}$/.test(String(sheet))) throw new Error("汽水歌单 ID 无效");
  page = page || 1;
  if (page === 1 && !sheetCursors[sheet]) sheetCursors[sheet] = { 1: "" };
  const cursors = sheetCursors[sheet];
  if (!cursors || cursors[page] === undefined) throw new Error("汽水歌单分页状态已失效");
  const vars = env.getUserVariables();
  const data = await accountGet("/luna/pc/playlist/detail", Object.assign(pcParams(vars.device_id, vars.install_id), {
    version_name: "3.3.0", version_code: 30030000, playlist_id: sheet, cursor: cursors[page], count: 50,
  }));
  if (data.media_resources == null && !data.has_more && data.playlist) data.media_resources = [];
  if (!Array.isArray(data.media_resources)) throw new Error("汽水歌单内容响应无效");
  const songs = resourceSongs(data.media_resources);
  if (data.has_more) {
    const next = itemID(data.next_cursor);
    if (!next || next === cursors[page]) throw new Error("汽水歌单分页游标无效");
    cursors[page + 1] = next;
  }
  return { isEnd: !data.has_more, data: songs };
}

module.exports = {
  platform: "汽水音乐", author: "MusicGo", version: "1.1.1", description: "汽水音乐：扫码登录、搜索与歌词，完整音源受平台限制",
  supportedSearchType: ["music", "album", "sheet", "artist"], primaryKey: ["id"], cacheControl: "no-cache", hints: { builtin: true, logo: "https://is1-ssl.mzstatic.com/image/thumb/Purple211/v4/a1/6d/b4/a16db478-33e0-fc41-bc84-d5dd33f10762/AppIcon-0-0-1x_U007epad-0-1-0-85-220.png/512x512bb.jpg" },
  search, getMediaSource, getLyric, getAlbumInfo, getArtistWorks, getMusicSheetInfo,
  getLoginQR, pollLoginQR, getLoginStatus, getUserProfile, getUserSheets, getSheetSongs,
};
