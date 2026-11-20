// Copyright (c) 2013-2014 The btcsuite developers
// Copyright (c) 2015 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package util_test

import (
	"os/user"
	"path/filepath"
	"testing"
	"unicode"

	"github.com/BlockdagNetworkLabs/bdag/common/util"
)

// TestAppDataDir tests the API for AppDataDir to ensure it gives expected
// results for various operating systems.
func TestAppDataDir(t *testing.T) {
	// WSL and other non-Windows hosts may inherit Windows profile variables.
	// The cross-OS matrix exercises the missing-profile fallback explicitly.
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("APPDATA", "")

	// App name plus upper and lowercase variants.
	appName := "myapp"
	appNameUpper := string(unicode.ToUpper(rune(appName[0]))) + appName[1:]
	appNameLower := string(unicode.ToLower(rune(appName[0]))) + appName[1:]

	winLocal := "."
	winRoaming := "."

	// Get the home directory to use for testing expected results.
	var homeDir string
	usr, err := user.Current()
	if err != nil {
		t.Errorf("user.Current: %v", err)
		return
	}
	homeDir = usr.HomeDir

	// Mac app data directory.
	macAppData := filepath.Join(homeDir, "Library", "Application Support")

	tests := []struct {
		goos    string
		appName string
		roaming bool
		want    string
	}{
		// Various combinations of application name casing, leading
		// period, operating system, and roaming flags.
		{"windows", appNameLower, false, winLocal},
		{"windows", appNameUpper, false, winLocal},
		{"windows", "." + appNameLower, false, winLocal},
		{"windows", "." + appNameUpper, false, winLocal},
		{"windows", appNameLower, true, winRoaming},
		{"windows", appNameUpper, true, winRoaming},
		{"windows", "." + appNameLower, true, winRoaming},
		{"windows", "." + appNameUpper, true, winRoaming},
		{"linux", appNameLower, false, filepath.Join(homeDir, "."+appNameLower)},
		{"linux", appNameUpper, false, filepath.Join(homeDir, "."+appNameLower)},
		{"linux", "." + appNameLower, false, filepath.Join(homeDir, "."+appNameLower)},
		{"linux", "." + appNameUpper, false, filepath.Join(homeDir, "."+appNameLower)},
		{"darwin", appNameLower, false, filepath.Join(macAppData, appNameUpper)},
		{"darwin", appNameUpper, false, filepath.Join(macAppData, appNameUpper)},
		{"darwin", "." + appNameLower, false, filepath.Join(macAppData, appNameUpper)},
		{"darwin", "." + appNameUpper, false, filepath.Join(macAppData, appNameUpper)},
		{"openbsd", appNameLower, false, filepath.Join(homeDir, "."+appNameLower)},
		{"openbsd", appNameUpper, false, filepath.Join(homeDir, "."+appNameLower)},
		{"openbsd", "." + appNameLower, false, filepath.Join(homeDir, "."+appNameLower)},
		{"openbsd", "." + appNameUpper, false, filepath.Join(homeDir, "."+appNameLower)},
		{"freebsd", appNameLower, false, filepath.Join(homeDir, "."+appNameLower)},
		{"freebsd", appNameUpper, false, filepath.Join(homeDir, "."+appNameLower)},
		{"freebsd", "." + appNameLower, false, filepath.Join(homeDir, "."+appNameLower)},
		{"freebsd", "." + appNameUpper, false, filepath.Join(homeDir, "."+appNameLower)},
		{"netbsd", appNameLower, false, filepath.Join(homeDir, "."+appNameLower)},
		{"netbsd", appNameUpper, false, filepath.Join(homeDir, "."+appNameLower)},
		{"netbsd", "." + appNameLower, false, filepath.Join(homeDir, "."+appNameLower)},
		{"netbsd", "." + appNameUpper, false, filepath.Join(homeDir, "."+appNameLower)},
		{"plan9", appNameLower, false, filepath.Join(homeDir, appNameLower)},
		{"plan9", appNameUpper, false, filepath.Join(homeDir, appNameLower)},
		{"plan9", "." + appNameLower, false, filepath.Join(homeDir, appNameLower)},
		{"plan9", "." + appNameUpper, false, filepath.Join(homeDir, appNameLower)},
		{"unrecognized", appNameLower, false, filepath.Join(homeDir, "."+appNameLower)},
		{"unrecognized", appNameUpper, false, filepath.Join(homeDir, "."+appNameLower)},
		{"unrecognized", "." + appNameLower, false, filepath.Join(homeDir, "."+appNameLower)},
		{"unrecognized", "." + appNameUpper, false, filepath.Join(homeDir, "."+appNameLower)},

		// No application name provided, so expect current directory.
		{"windows", "", false, "."},
		{"windows", "", true, "."},
		{"linux", "", false, "."},
		{"darwin", "", false, "."},
		{"openbsd", "", false, "."},
		{"freebsd", "", false, "."},
		{"netbsd", "", false, "."},
		{"plan9", "", false, "."},
		{"unrecognized", "", false, "."},

		// Single dot provided for application name, so expect current
		// directory.
		{"windows", ".", false, "."},
		{"windows", ".", true, "."},
		{"linux", ".", false, "."},
		{"darwin", ".", false, "."},
		{"openbsd", ".", false, "."},
		{"freebsd", ".", false, "."},
		{"netbsd", ".", false, "."},
		{"plan9", ".", false, "."},
		{"unrecognized", ".", false, "."},
	}

	t.Logf("Running %d tests", len(tests))
	for i, test := range tests {
		ret := util.TstAppDataDir(test.goos, test.appName, test.roaming)
		if ret != test.want {
			t.Errorf("appDataDir #%d (%s) does not match - "+
				"expected got %s, want %s", i, test.goos, ret,
				test.want)
			continue
		}
	}
}

func TestAppDataDirWindowsProfiles(t *testing.T) {
	profileRoot := t.TempDir()
	local := filepath.Join(profileRoot, "local")
	roaming := filepath.Join(profileRoot, "roaming")
	tests := []struct {
		name        string
		localPath   string
		roamingPath string
		useRoaming  bool
		want        string
	}{
		{"local", local, roaming, false, filepath.Join(local, "Myapp")},
		{"roaming", local, roaming, true, filepath.Join(roaming, "Myapp")},
		{"local-falls-back-to-roaming", "", roaming, false, filepath.Join(roaming, "Myapp")},
		{"roaming-without-local", "", roaming, true, filepath.Join(roaming, "Myapp")},
		{"local-without-roaming", local, "", false, filepath.Join(local, "Myapp")},
		{"roaming-does-not-fall-back-to-local", local, "", true, "."},
		{"neither-local", "", "", false, "."},
		{"neither-roaming", "", "", true, "."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("LOCALAPPDATA", test.localPath)
			t.Setenv("APPDATA", test.roamingPath)
			if got := util.TstAppDataDir("windows", "myapp", test.useRoaming); got != test.want {
				t.Errorf("appDataDir(windows): got %q, want %q", got, test.want)
			}
		})
	}
}
