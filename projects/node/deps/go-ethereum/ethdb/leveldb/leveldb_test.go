// Copyright 2019 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package leveldb

import (
	"testing"

	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/dbtest"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"
)

func TestLevelDB(t *testing.T) {
	t.Run("DatabaseSuite", func(t *testing.T) {
		dbtest.TestDatabaseSuite(t, func() ethdb.KeyValueStore {
			db, err := leveldb.Open(storage.NewMemStorage(), nil)
			if err != nil {
				t.Fatal(err)
			}
			return &Database{
				db: db,
			}
		})
	})
}

func TestSyncKeyValue(t *testing.T) {
	storage := storage.NewMemStorage()
	raw, err := leveldb.Open(storage, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := &Database{db: raw}
	t.Cleanup(func() { db.Close() })

	if err := db.Put([]byte("checkpoint"), []byte("durable")); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncKeyValue(); err != nil {
		t.Fatal(err)
	}
	barrierKey := []byte("\xffblockdag-recovery-sync-barrier")
	if _, err := db.Get(barrierKey); err != leveldb.ErrNotFound {
		t.Fatalf("temporary sync barrier remains in database: %v", err)
	}
	if err := db.Put(barrierKey, []byte("existing")); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncKeyValue(); err != nil {
		t.Fatal(err)
	}
	value, err := db.Get(barrierKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "existing" {
		t.Fatalf("sync barrier value = %q, want existing", value)
	}
}

func BenchmarkLevelDB(b *testing.B) {
	dbtest.BenchDatabaseSuite(b, func() ethdb.KeyValueStore {
		db, err := leveldb.Open(storage.NewMemStorage(), nil)
		if err != nil {
			b.Fatal(err)
		}
		return &Database{
			db: db,
		}
	})
}
