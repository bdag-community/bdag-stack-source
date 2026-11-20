// Copyright (c) 2013-2014 The btcsuite developers
// Copyright (c) 2015-2017 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package version

// These constants define the application version and follow the semantic
// versioning 2.0.0 spec (http://semver.org/).
const (
	Major uint = 1
	Minor uint = 0
	Patch uint = 8
)

// ReleaseTag is injected at build time from the exact git tag on HEAD.
// Supported node builds must fail if this is not populated.
var ReleaseTag = ""

// SourceCommit is injected into component packages from a clean source tree.
// It remains empty for ordinary source builds.
var SourceCommit = ""

// String returns the exact release tag that was injected at build time.
func String() string {
	return ReleaseTag
}
