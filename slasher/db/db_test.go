package db

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/prysmaticlabs/prysm/shared/testutil"
)

var _ = Database(&Store{})

// setupDB instantiates and returns a SlasherDB instance.
func setupDB(t testing.TB) *Store {

	randPath, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		t.Fatalf("Could not generate random file path: %v", err)
	}
	path := path.Join(testutil.TempDir(), fmt.Sprintf("/%d", randPath))
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("Failed to remove directory: %v", err)
	}
	db, err := NewDB(path)

	if err != nil {
		t.Fatalf("Failed to instantiate DB: %v", err)
	}
	return db
}

// teardownDB cleans up a test BeaconDB instance.
func teardownDB(t testing.TB, db *SlasherDB) {
	if err := db.Close(); err != nil {
		t.Fatalf("Failed to close database: %v", err)
	}
	if err := os.RemoveAll(db.DatabasePath); err != nil {
		t.Fatalf("Failed to remove directory: %v", err)
	}
}

func TestClearDB(t *testing.T) {
	slasherDB := setupDB(t)
	path := strings.TrimSuffix(slasherDB.DatabasePath, "beaconchain.db")
	if err := slasherDB.ClearDB(path); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(beaconDB.DatabasePath); !os.IsNotExist(err) {
		t.Fatalf("db wasnt cleared %v", err)
	}
}