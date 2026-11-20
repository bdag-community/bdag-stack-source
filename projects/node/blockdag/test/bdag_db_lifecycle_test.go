package test

import (
	"errors"
	"testing"
)

type lifecycleDBProbe struct {
	initErr error
	initN   int
	closeN  int
}

func (db *lifecycleDBProbe) Init() error {
	db.initN++
	return db.initErr
}

func (db *lifecycleDBProbe) Close() {
	db.closeN++
}

func TestInitBlockDBClosesOnceAfterInitFailure(t *testing.T) {
	wantErr := errors.New("init failed")
	db := &lifecycleDBProbe{initErr: wantErr}

	if err := initBlockDB(db); !errors.Is(err, wantErr) {
		t.Fatalf("initBlockDB() error = %v, want %v", err, wantErr)
	}
	if db.initN != 1 {
		t.Fatalf("Init calls = %d, want 1", db.initN)
	}
	if db.closeN != 1 {
		t.Fatalf("Close calls = %d, want 1", db.closeN)
	}
}

func TestInitBlockDBKeepsSuccessfulHandleOpen(t *testing.T) {
	db := new(lifecycleDBProbe)

	if err := initBlockDB(db); err != nil {
		t.Fatalf("initBlockDB() error = %v", err)
	}
	if db.initN != 1 {
		t.Fatalf("Init calls = %d, want 1", db.initN)
	}
	if db.closeN != 0 {
		t.Fatalf("Close calls = %d, want 0", db.closeN)
	}
}
