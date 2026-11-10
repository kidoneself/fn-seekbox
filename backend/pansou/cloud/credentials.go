package cloud

import (
	"context"
	"errors"
	"sync"
)

type credentialCommitKey struct{}
type credentialCommit func(Credentials) error

func withCredentialCommit(ctx context.Context, commit credentialCommit) context.Context {
	return context.WithValue(ctx, credentialCommitKey{}, commit)
}

// CommitCredentials must succeed before a provider starts using a rotated
// refresh token. The callback never exposes credentials to HTTP responses.
func CommitCredentials(ctx context.Context, credentials Credentials) error {
	commit, ok := ctx.Value(credentialCommitKey{}).(credentialCommit)
	if !ok {
		return errors.New("更新后的网盘登录状态无法保存，请重新连接")
	}
	return commit(credentials)
}

func (s *Service) credentialContext(ctx context.Context, owner string, account *storedAccount) context.Context {
	return withCredentialCommit(ctx, func(credentials Credentials) error {
		secret, err := s.store.encrypt(owner, account.ID, credentials)
		if err != nil {
			return err
		}
		err = s.store.update(owner, func(state *userState) error {
			current, ok := state.Accounts[account.ID]
			if !ok {
				return ErrNotFound
			}
			if current.Secret != account.Secret {
				return ErrBusy
			}
			current.Secret, current.UpdatedAt = secret, now()
			state.Accounts[account.ID] = current
			return nil
		})
		if err == nil {
			account.Secret = secret
		}
		return err
	})
}

// Serialize calls for the same account, including browsing and background
// jobs, so two requests cannot rotate the same refresh token concurrently.
func (s *Service) lockAccount(ctx context.Context, owner, id string) (func(), error) {
	return lockOperation(ctx, &s.accountLocks, digest(owner, id))
}

func lockOperation(ctx context.Context, locks *sync.Map, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, _ := locks.LoadOrStore(key, make(chan struct{}, 1))
	lock := value.(chan struct{})
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
