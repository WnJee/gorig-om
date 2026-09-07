package alert

import (
	"github.com/gin-gonic/gin"
	"github.com/jom-io/gorig/apix"
	"github.com/jom-io/gorig/global/consts"
)

func GetConfig(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	cfg := S().GetConfig()
	apix.HandleData(ctx, consts.CurdSelectFailCode, cfg, nil)
}

func SaveConfig(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	var cfg AlertConfig
	if err := apix.BindParams(ctx, &cfg, true); err != nil {
		return
	}
	e := S().SaveConfig(cfg)
	apix.HandleData(ctx, consts.CurdUpdateFailCode, nil, e)
}

func TestSend(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	err := S().TestSend(ctx)
	apix.HandleData(ctx, consts.CurdSelectFailCode, "Test alert sent successfully", err)
}
