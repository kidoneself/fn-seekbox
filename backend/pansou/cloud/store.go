package cloud

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"pansou/util/json"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

var usersBucket = []byte("users")
var marksBucket = []byte("transfers")

type storedAccount struct {
	Account
	Secret string `json:"encrypted_credentials"`
}
type jobRecord struct {
	Job
	Request TransferRequest `json:"request"`
}
type userState struct {
	Accounts      map[string]storedAccount `json:"accounts"`
	Subscriptions map[string]Subscription  `json:"subscriptions"`
	Jobs          map[string]jobRecord     `json:"jobs"`
}
type store struct {
	db   *bolt.DB
	aead cipher.AEAD
}

func openStore(dir string) (*store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "credentials.key")
	key, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		// Never silently replace a lost key for an existing database.
		if _, statErr := os.Stat(filepath.Join(dir, "cloud.db")); !os.IsNotExist(statErr) {
			return nil, errors.New("网盘凭据密钥缺失，请恢复原 credentials.key")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, createErr := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if createErr != nil {
			return nil, createErr
		}
		_, err = f.Write(key)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("网盘凭据密钥无效")
	}
	if err = os.Chmod(keyPath, 0600); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, "cloud.db")
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(dbPath, 0600); err != nil {
		db.Close()
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		if _, e := tx.CreateBucketIfNotExists(usersBucket); e != nil {
			return e
		}
		_, e := tx.CreateBucketIfNotExists(marksBucket)
		return e
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &store{db: db, aead: aead}, nil
}

func readState(tx *bolt.Tx, owner string) (userState, error) {
	s := userState{}
	if raw := tx.Bucket(usersBucket).Get([]byte(owner)); raw != nil {
		if err := json.Unmarshal(raw, &s); err != nil {
			return s, err
		}
	}
	if s.Accounts == nil {
		s.Accounts = map[string]storedAccount{}
	}
	if s.Subscriptions == nil {
		s.Subscriptions = map[string]Subscription{}
	}
	if s.Jobs == nil {
		s.Jobs = map[string]jobRecord{}
	}
	return s, nil
}

func (s *store) read(owner string) (userState, error) {
	var state userState
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		state, err = readState(tx, owner)
		return err
	})
	return state, err
}

func (s *store) update(owner string, fn func(*userState) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		state, err := readState(tx, owner)
		if err != nil {
			return err
		}
		if err = fn(&state); err != nil {
			return err
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		return tx.Bucket(usersBucket).Put([]byte(owner), raw)
	})
}

func (s *store) owners() ([]string, error) {
	var owners []string
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(usersBucket).ForEach(func(k, _ []byte) error {
			owners = append(owners, string(k))
			return nil
		})
	})
	return owners, err
}

func (s *store) encrypt(owner, accountID string, credentials Credentials) (string, error) {
	raw, err := json.Marshal(credentials)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := s.aead.Seal(nonce, nonce, raw, []byte(owner+"\x00"+accountID))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *store) decrypt(owner string, account storedAccount) (Credentials, error) {
	sealed, err := base64.StdEncoding.DecodeString(account.Secret)
	if err != nil || len(sealed) < s.aead.NonceSize() {
		return nil, errors.New("网盘凭据无法解密")
	}
	raw, err := s.aead.Open(nil, sealed[:s.aead.NonceSize()], sealed[s.aead.NonceSize():], []byte(owner+"\x00"+account.ID))
	if err != nil {
		return nil, errors.New("网盘凭据无法解密")
	}
	var credentials Credentials
	if err = json.Unmarshal(raw, &credentials); err != nil {
		return nil, errors.New("网盘凭据无法读取")
	}
	return credentials, nil
}

func (s *store) mark(key string) (string, error) {
	var value string
	err := s.db.View(func(tx *bolt.Tx) error { value = string(tx.Bucket(marksBucket).Get([]byte(key))); return nil })
	return value, err
}
func (s *store) putMark(key, value string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if value == "" {
			return tx.Bucket(marksBucket).Delete([]byte(key))
		}
		return tx.Bucket(marksBucket).Put([]byte(key), []byte(value))
	})
}

// Commit file journal changes and the displayed count in one durable transaction.
func (s *store) saveJobMarks(owner string, job jobRecord, marks map[string]string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		state, err := readState(tx, owner)
		if err != nil {
			return err
		}
		if _, ok := state.Jobs[job.ID]; !ok {
			return ErrNotFound
		}
		state.Jobs[job.ID] = job
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if err = tx.Bucket(usersBucket).Put([]byte(owner), raw); err != nil {
			return err
		}
		for key, value := range marks {
			if value == "" {
				err = tx.Bucket(marksBucket).Delete([]byte(key))
			} else {
				err = tx.Bucket(marksBucket).Put([]byte(key), []byte(value))
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}
