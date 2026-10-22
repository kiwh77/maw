package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithTxConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maw.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	const writers = 8
	const rounds = 20
	var okCount atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				key := fmt.Sprintf("w%d-r%d", id, r)
				err := WithTx(path, func(tx *sql.Tx) error {
					_, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`, key, "1")
					if err != nil {
						return err
					}
					// Hold the write lock briefly to force contention.
					time.Sleep(5 * time.Millisecond)
					_, err = tx.Exec(`INSERT INTO events(ts, project_id, actor, type, task_id, data_json) VALUES (?,?,?,?,?,?)`,
						UTCNow(), nil, "test", "busy_probe", nil, "{}")
					return err
				})
				if err != nil {
					t.Errorf("writer %d round %d: %v", id, r, err)
					return
				}
				okCount.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	want := int64(writers * rounds)
	if got := okCount.Load(); got != want {
		t.Fatalf("completed %d/%d writes", got, want)
	}
}

func TestIsBusy(t *testing.T) {
	if IsBusy(nil) {
		t.Fatal("nil should not be busy")
	}
	if !IsBusy(errString("database is locked (5)")) {
		t.Fatal("expected busy for locked message")
	}
	if IsBusy(errString("no such table: foo")) {
		t.Fatal("unrelated error must not be busy")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
