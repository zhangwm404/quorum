package def

import (
	quorumpb "github.com/rumsystem/quorum/pkg/pb"
)

type GroupIface interface {
	SendRawTrx(trx *quorumpb.Trx) (string, error)
	GetTrx(trxId string) (*quorumpb.Trx, []int64, error)
	GetTrxFromCache(trxId string) (*quorumpb.Trx, []int64, error)
	GetRexSyncerStatus() string
}

type RexSyncResult struct {
	Provider              string
	FromBlock             uint64
	BlockProvided         int32
	SyncResult            string
	LastSyncTaskTimestamp int64
	NextSyncTaskTimeStamp int64
}
