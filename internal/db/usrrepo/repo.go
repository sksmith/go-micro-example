package usrrepo

import (
	"context"

	"github.com/jackc/pgx/v4"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/core"
	"github.com/sksmith/go-micro-example/internal/core/user"
	"github.com/sksmith/go-micro-example/internal/db"

	lru "github.com/hashicorp/golang-lru"
)

type dbRepo struct {
	conn core.Conn
	c    *lru.Cache
}

func NewPostgresRepo(conn core.Conn) user.Repository {
	l, err := lru.New(256)
	if err != nil {
		log.Warn().Err(err).Msg("unable to configure cache")
	}
	return &dbRepo{
		conn: conn,
		c:    l,
	}
}

func (r *dbRepo) Create(ctx context.Context, user *user.User, txs ...core.Transaction) error {
	m := db.StartMetric("Create")
	tx := r.conn
	if len(txs) > 0 {
		tx = txs[0]
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO users (username, password, is_admin, created_at)
                      VALUES ($1, $2, $3, $4);`,
		user.Username, user.HashedPassword, user.IsAdmin, user.Created)
	if err != nil {
		m.Complete(err)
		return err
	}
	r.cache(*user)
	m.Complete(nil)
	return nil
}

func (r *dbRepo) Get(ctx context.Context, username string, txs ...core.Transaction) (user.User, error) {
	m := db.StartMetric("GetUser")
	tx := r.conn
	if len(txs) > 0 {
		tx = txs[0]
	}

	u, ok := r.getcache(username)
	if ok {
		return u, nil
	}
	err := tx.QueryRow(ctx, `
		SELECT username, password, is_admin, created_at 
			FROM users WHERE username = $1`, username).
		Scan(&u.Username, &u.HashedPassword, &u.IsAdmin, &u.Created)

	if err != nil {
		m.Complete(err)
		if err == pgx.ErrNoRows {
			return user.User{}, errors.WithStack(core.ErrNotFound)
		}
		return user.User{}, errors.WithStack(err)
	}

	r.cache(u)
	m.Complete(nil)
	return u, nil
}

func (r *dbRepo) Delete(ctx context.Context, username string, txs ...core.Transaction) error {
	m := db.StartMetric("DeleteUser")
	tx := r.conn
	if len(txs) > 0 {
		tx = txs[0]
	}

	_, err := tx.Exec(ctx, `DELETE FROM users WHERE username = $1`, username)

	if err != nil {
		m.Complete(err)
		if err == pgx.ErrNoRows {
			return errors.WithStack(core.ErrNotFound)
		}
		return errors.WithStack(err)
	}

	r.uncache(username)
	m.Complete(nil)
	return nil
}

func (r *dbRepo) cache(u user.User) {
	if r.c == nil {
		return
	}
	r.c.Add(u.Username, u)
}

func (r *dbRepo) uncache(username string) {
	if r.c == nil {
		return
	}
	r.c.Remove(username)
}

func (r *dbRepo) getcache(username string) (user.User, bool) {
	if r.c == nil {
		return user.User{}, false
	}

	v, ok := r.c.Get(username)
	if !ok {
		return user.User{}, false
	}
	u, ok := v.(user.User)
	return u, ok
}
