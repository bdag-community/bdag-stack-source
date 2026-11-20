package peers

import (
	"fmt"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
)

type BadResponse struct {
	ID   uint64
	Time time.Time
	Err  *common.Error
}

func (br *BadResponse) String() string {
	return fmt.Sprintf("id:%d time:%s err:%s", br.ID, br.Time.Format(time.RFC3339), br.Err.String())
}
