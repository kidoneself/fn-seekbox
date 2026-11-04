package cloud

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreCredentialsAreEncryptedBoundToOwnerAndAccount(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.db.Close() })
	credentials := Credentials{"cookie": "BDUSS=private-test-cookie; STOKEN=private-test-token"}
	first, err := st.encrypt("alice", "account-1", credentials)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.encrypt("alice", "account-1", credentials)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("encryption reused a nonce for identical credentials")
	}
	account := storedAccount{Account: Account{ID: "account-1", Provider: "quark", Name: "Alice"}, Secret: first}
	if err := st.update("alice", func(state *userState) error { state.Accounts[account.ID] = account; return nil }); err != nil {
		t.Fatal(err)
	}
	decrypted, err := st.decrypt("alice", account)
	if err != nil || decrypted["cookie"] != credentials["cookie"] {
		t.Fatalf("credentials did not round trip: err=%v", err)
	}
	if _, err := st.decrypt("bob", account); err == nil {
		t.Fatal("another owner could decrypt credentials")
	}
	account.ID = "account-2"
	if _, err := st.decrypt("alice", account); err == nil {
		t.Fatal("ciphertext could be moved to a different account")
	}
	account.ID = "account-1"
	account.Secret = first[:len(first)/2] + "AAAA" + first[len(first)/2+4:]
	if _, err := st.decrypt("alice", account); err == nil {
		t.Fatal("tampered ciphertext decrypted")
	}
	data, err := os.ReadFile(filepath.Join(dir, "cloud.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-test-cookie", "private-test-token", "BDUSS="} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("database contains plaintext credentials")
		}
	}
	for filename, mode := range map[string]os.FileMode{"": 0700, "credentials.key": 0600, "cloud.db": 0600} {
		info, err := os.Stat(filepath.Join(dir, filename))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("insecure mode for %s: %o", filename, info.Mode().Perm())
		}
	}
}

func TestStoreReopensWithoutLosingCredentialsOrProgress(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := st.encrypt("alice", "a", Credentials{"cookie": "restart-private-cookie"})
	if err != nil {
		t.Fatal(err)
	}
	account := storedAccount{Account: Account{ID: "a", Provider: "quark", Name: "Account"}, Secret: secret}
	if err := st.update("alice", func(state *userState) error { state.Accounts["a"] = account; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := st.putMark("uncertain-file", "pending"); err != nil {
		t.Fatal(err)
	}
	if err := st.putMark("confirmed-file", "done"); err != nil {
		t.Fatal(err)
	}
	if err := st.db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.db.Close() })
	state, err := st.read("alice")
	if err != nil {
		t.Fatal(err)
	}
	creds, err := st.decrypt("alice", state.Accounts["a"])
	if err != nil || creds["cookie"] != "restart-private-cookie" {
		t.Fatalf("could not decrypt after restart: %v", err)
	}
	for key, want := range map[string]string{"uncertain-file": "pending", "confirmed-file": "done"} {
		got, err := st.mark(key)
		if err != nil || got != want {
			t.Fatalf("journal lost after restart: %s=%q err=%v", key, got, err)
		}
	}
	other, err := st.read("bob")
	if err != nil || len(other.Accounts) != 0 {
		t.Fatal("owner state leaked after reopening")
	}
}

func TestStoreRefusesMissingKeyForExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "credentials.key")); err != nil {
		t.Fatal(err)
	}
	if reopened, err := openStore(dir); err == nil {
		_ = reopened.db.Close()
		t.Fatal("silently generated a new key for an existing database")
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.key")); !os.IsNotExist(err) {
		t.Fatal("replaced missing key before recovery")
	}
}
