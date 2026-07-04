package clock

import (
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

// Real is the production clock.
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

var _ core.Clock = Real{}
