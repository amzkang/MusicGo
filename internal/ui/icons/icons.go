package icons

import (
	_ "embed"
	"github.com/egoist/mygo/ui"
)

var (
	// Navigation & System
	//go:embed svg/logo.svg
	logoData []byte
	//go:embed svg/chevron-left.svg
	chevronLeftData []byte
	//go:embed svg/chevron-right.svg
	chevronRightData []byte
	//go:embed svg/chevron-down.svg
	chevronDownData []byte
	//go:embed svg/magnifying-glass.svg
	searchData []byte
	//go:embed svg/sparkles.svg
	sparklesData []byte
	//go:embed svg/cog-8-tooth.svg
	settingsData []byte
	//go:embed svg/minus.svg
	minusData []byte
	//go:embed svg/square.svg
	squareData []byte
	//go:embed svg/x-mark.svg
	closeData []byte

	// Window controls, drawn as the window buttons of the original project:
	// a short dash to minimize, a square to maximize, two squares to restore,
	// and a cross mark to close.
	//go:embed svg/window-minimize.svg
	windowMinimizeData []byte
	//go:embed svg/window-maximize.svg
	windowMaximizeData []byte
	//go:embed svg/window-restore.svg
	windowRestoreData []byte
	//go:embed svg/window-close.svg
	windowCloseData []byte

	// Sidebar
	//go:embed svg/trophy.svg
	trophyData []byte
	//go:embed svg/fire.svg
	fireData []byte
	//go:embed svg/folder-open.svg
	folderData []byte
	//go:embed svg/code-bracket-square.svg
	pluginData []byte
	//go:embed svg/clock.svg
	historyData []byte
	//go:embed svg/heart.svg
	heartData []byte
	//go:embed svg/heart-outline.svg
	heartOutlineData []byte
	//go:embed svg/plus.svg
	plusData []byte
	//go:embed svg/musical-note.svg
	noteData []byte

	// Playback Controls
	//go:embed svg/skip-left.svg
	skipLeftData []byte
	//go:embed svg/play.svg
	playData []byte
	//go:embed svg/pause.svg
	pauseData []byte
	//go:embed svg/skip-right.svg
	skipRightData []byte
	//go:embed svg/speaker-wave.svg
	volumeData []byte
	//go:embed svg/speaker-x-mark.svg
	volumeMuteData []byte
	//go:embed svg/lyric.svg
	lyricData []byte
	//go:embed svg/repeat-song.svg
	repeatData []byte
	//go:embed svg/repeat-song-1.svg
	repeatOneData []byte
	//go:embed svg/shuffle.svg
	shuffleData []byte
	//go:embed svg/list-bullet.svg
	playlistData []byte
	//go:embed svg/lock-open.svg
	lockOpenData []byte
	//go:embed svg/lock-closed.svg
	lockClosedData []byte
	//go:embed svg/album.svg
	albumData []byte
	//go:embed svg/headphone.svg
	headphoneData []byte

	// Others
	//go:embed svg/trash.svg
	trashData []byte
	//go:embed svg/check.svg
	checkData []byte
	//go:embed svg/array-download-tray.svg
	downloadData []byte
)

var (
	Logo         = ui.MustParseSVG(logoData)
	ChevronLeft  = ui.MustParseSVG(chevronLeftData)
	ChevronRight = ui.MustParseSVG(chevronRightData)
	ChevronDown  = ui.MustParseSVG(chevronDownData)
	Search       = ui.MustParseSVG(searchData)
	Sparkles     = ui.MustParseSVG(sparklesData)
	Settings     = ui.MustParseSVG(settingsData)
	Minus        = ui.MustParseSVG(minusData)
	Square       = ui.MustParseSVG(squareData)
	Close        = ui.MustParseSVG(closeData)

	WindowMinimize = ui.MustParseSVG(windowMinimizeData)
	WindowMaximize = ui.MustParseSVG(windowMaximizeData)
	WindowRestore  = ui.MustParseSVG(windowRestoreData)
	WindowClose    = ui.MustParseSVG(windowCloseData)

	Trophy       = ui.MustParseSVG(trophyData)
	Fire         = ui.MustParseSVG(fireData)
	Folder       = ui.MustParseSVG(folderData)
	Plugin       = ui.MustParseSVG(pluginData)
	History      = ui.MustParseSVG(historyData)
	Heart        = ui.MustParseSVG(heartData)
	HeartOutline = ui.MustParseSVG(heartOutlineData)
	Plus         = ui.MustParseSVG(plusData)
	Note         = ui.MustParseSVG(noteData)

	SkipLeft   = ui.MustParseSVG(skipLeftData)
	Play       = ui.MustParseSVG(playData)
	Pause      = ui.MustParseSVG(pauseData)
	SkipRight  = ui.MustParseSVG(skipRightData)
	Volume     = ui.MustParseSVG(volumeData)
	VolumeMute = ui.MustParseSVG(volumeMuteData)
	Lyric      = ui.MustParseSVG(lyricData)
	Repeat     = ui.MustParseSVG(repeatData)
	RepeatOne  = ui.MustParseSVG(repeatOneData)
	Shuffle    = ui.MustParseSVG(shuffleData)
	Playlist   = ui.MustParseSVG(playlistData)
	LockOpen   = ui.MustParseSVG(lockOpenData)
	LockClosed = ui.MustParseSVG(lockClosedData)
	Album      = ui.MustParseSVG(albumData)
	Headphone  = ui.MustParseSVG(headphoneData)

	Trash    = ui.MustParseSVG(trashData)
	Check    = ui.MustParseSVG(checkData)
	Download = ui.MustParseSVG(downloadData)
)
