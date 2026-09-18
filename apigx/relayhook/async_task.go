package relayhook

import (
	"strconv"

	"github.com/gin-gonic/gin"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// AsyncTask 状态（依据 docs/stage0/schema-draft.md §5.23）。
const (
	AsyncTaskStatusPendingSubmit int = 0
	AsyncTaskStatusRunning       int = 1
	AsyncTaskStatusSucceeded     int = 2
	AsyncTaskStatusFailed        int = 3
	AsyncTaskStatusCanceled      int = 4
	AsyncTaskStatusUnknown       int = 5
	AsyncTaskStatusReconciling   int = 6
)

// RecordAsyncTaskSnapshot 在**任务提交并结算成功之后**写入我们自己的任务归属快照。
//
// 为什么需要它（§5.23 的注释即为此）：底座的 tasks 表只记它自己关心的字段；
// 一旦上游 Key 轮换、授权被撤、分享者被封禁，我们仍要能**定位原任务**——
// 因此这里把"当时是谁的、哪个渠道/供给、哪把 Key、哪个价格版本"固化成快照。
//
// 纪律（与 usage_record 一致）：
//   - 只**追加**我们自己的行，绝不触碰底座扣费；
//   - base_task_id 唯一 ⇒ 幂等，重复提交不产生第二行；
//   - 快照列只写一次，**不回写**；状态列只允许单向推进；
//   - 解析不到归属就留 0（如实表达"当时没记录到"），不猜；
//   - 任何失败只记日志，**不影响任务结果**。
func RecordAsyncTaskSnapshot(c *gin.Context, relayInfo *relaycommon.RelayInfo, baseTaskID int64, upstreamTaskID, action string) {
	if c == nil || relayInfo == nil || baseTaskID <= 0 {
		return
	}
	db := apigxmodel.DefaultDB()
	if db == nil {
		return
	}

	// 幂等：已有该底座任务的快照就不再写（唯一约束兜底）
	var existing apigxmodel.AsyncTask
	if err := db.Where("base_task_id = ?", baseTaskID).First(&existing).Error; err == nil {
		return
	}

	owner, channelID, offeringID, keyID := resolveUsageAttribution(db, c)
	now := common.GetTimestamp()

	task := &apigxmodel.AsyncTask{
		PublicTaskID:   "apigx_" + strconv.FormatInt(baseTaskID, 10),
		BaseTaskID:     baseTaskID,
		UpstreamTaskID: upstreamTaskID,
		UserID:         int64(relayInfo.UserId),
		ApiKeyID:       int64(relayInfo.TokenId),
		OfferingID:     offeringID,
		ChannelID:      channelID,
		UpstreamKeyID:  keyID,
		OwnerUserID:    owner,
		Action:         action,
		Status:         AsyncTaskStatusRunning, // 提交并结算成功那一刻，如实记为"运行中"
		SubmitAt:       now,
		BillingSettled: true, // 提交时已完成结算（这是本快照的写入时机）
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.Create(task).Error; err != nil {
		common.SysError("relayhook: record async task snapshot failed: " + err.Error())
	}
}
