package main

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestMain hashes test passwords at bcrypt's minimum cost. Cost 12 makes the suite take minutes under -race,
// and no test depends on hash cost. The production value stays in users.go.
func TestMain(m *testing.M) {
	bcryptCost = bcrypt.MinCost
	os.Exit(m.Run())
}
