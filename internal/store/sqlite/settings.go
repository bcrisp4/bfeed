package sqlite

import (
	"context"

	"github.com/bcrisp4/bfeed/internal/store/sqlite/sqlc"
)

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	v, err := s.q.GetAppSetting(ctx, key)
	if err != nil {
		return "", mapErr(err)
	}
	return v, nil
}

func (s *Store) PutSetting(ctx context.Context, key, value string) error {
	return mapErr(s.q.PutAppSetting(ctx, sqlc.PutAppSettingParams{Key: key, Value: value}))
}
