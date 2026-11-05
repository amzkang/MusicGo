// Package builtin holds the plugin sources that ship inside the binary, so
// they need no import by the user and always win over stale copies in the
// data directory (same platform: the later load replaces the earlier one).
package builtin

import _ "embed"

// KugouConcept is the source of the 酷狗概念版 plugin. This file is the
// single source of truth; the data-dir copies were removed.
//
//go:embed kugou_concept.js
var KugouConcept []byte

// Platform is the platform the source exports; it is known up front, so the
// stored user variables can be injected in one load, without the two-phase
// load the file plugins need.
const Platform = "酷狗概念版"
