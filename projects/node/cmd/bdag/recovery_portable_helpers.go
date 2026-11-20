package main

import "strings"

// equalHex compares already-decoded hexadecimal identity fields without
// assigning authority to presentation differences such as case or whitespace.
func equalHex(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}
