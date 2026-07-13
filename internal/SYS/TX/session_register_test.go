package TX_test

import (
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SE"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
)

func init() {
	SY.RegisterSession(func(e *SY.Engine) AP.Session { return SE.NewSession(e) })
}
