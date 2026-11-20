package rawdb

import "testing"

func TestReadDAGTipsMissingKeyDoesNotPanic(t *testing.T) {
	db := NewMemoryDatabase()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ReadDAGTips panicked for missing key: %v", r)
		}
	}()

	if tips := ReadDAGTips(db); tips != nil {
		t.Fatalf("expected nil tips for missing key, got %v", tips)
	}
}
