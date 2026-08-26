package app

import (
	"github.com/gin-gonic/gin"
	"github.com/jom-io/gorig/apix"
	"github.com/jom-io/gorig/global/consts"
)

func Restart(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	err := App.Restart(ctx, "", nil)
	apix.HandleData(ctx, consts.CurdUpdateFailCode, nil, err)
}

func ReStared(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	startID, _ := apix.GetParamForce(ctx, "startID")
	itemID, _ := apix.GetParamStr(ctx, "itemID")
	src, _ := apix.GetParamStr(ctx, "src")
	pid, _ := apix.GetParamStr(ctx, "pid")
	App.RestartSuccess(ctx, startID, itemID, pid, StartSrc(src))
	apix.HandleData(ctx, consts.CurdUpdateFailCode, nil, nil)
}

func Stop(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	err := App.Stop(ctx)
	apix.HandleData(ctx, consts.CurdUpdateFailCode, nil, err)
}

func RestartLogs(ctx *gin.Context) {
	defer apix.HandlePanic(ctx)
	pageReq, _ := apix.GetPageReq(ctx)
	status, err := ReStartPage(ctx, pageReq.Page, pageReq.Size)
	apix.HandleData(ctx, consts.CurdSelectFailCode, status, err)
}
