// Package apigxroute 是 APIGX 接口的路由注册入口。
//
// 上游 `router/` 里只需要一处几行的接线；所有具体路由都集中在这里，
// 便于「以新增文件为主、少改上游」的维护策略（docs/architecture/baseline.md §3 E1）。
package apigxroute

import (
	apigxapi "github.com/QuantumNous/new-api/apigx/api"
	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// BasePath 是 APIGX 接口的挂载前缀（最终路径形如 /api/apigx/share-groups）。
const BasePath = "/apigx"

// Register 把 APIGX 的分享者接口挂到传入的路由组上。
//
// 调用方负责挂认证中间件（UserAuth）；本函数只负责路由与处理器。
// 数据库句柄由 `apigxmodel.Migrate` 在启动时捕获（见其注释），因此这里不需要额外参数。
func Register(apiRouter *gin.RouterGroup) {
	if cleaned, err := channelpool.ReconcileDeletedShareResources(apigxmodel.DefaultDB()); err != nil {
		common.SysError("apigx: reconcile deleted share resources failed: " + err.Error())
	} else if cleaned > 0 {
		common.SysLog("apigx: reconciled deleted share resources")
	}
	apigxapi.Register(apiRouter.Group(BasePath), apigxmodel.DefaultDB())
}
