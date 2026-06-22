package imgproxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/bcrisp4/bfeed/internal/core"
)

const settingKey = "image_proxy_secret"

// ResolveSecret returns the HMAC key: the operator env value if non-empty; else
// the key persisted in app_settings; else a freshly generated 32-byte key,
// hex-persisted so it is stable across restarts.
func ResolveSecret(ctx context.Context, store core.SettingStore, envSecret string) ([]byte, error) {
	if envSecret != "" {
		return []byte(envSecret), nil
	}
	v, err := store.GetSetting(ctx, settingKey)
	switch {
	case err == nil:
		key, derr := hex.DecodeString(v)
		if derr != nil {
			return nil, fmt.Errorf("decode stored image proxy secret: %w", derr)
		}
		return key, nil
	case errors.Is(err, core.ErrNotFound):
		key := make([]byte, 32)
		if _, rerr := rand.Read(key); rerr != nil {
			return nil, fmt.Errorf("generate image proxy secret: %w", rerr)
		}
		if perr := store.PutSetting(ctx, settingKey, hex.EncodeToString(key)); perr != nil {
			return nil, fmt.Errorf("persist image proxy secret: %w", perr)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("read image proxy secret: %w", err)
	}
}
