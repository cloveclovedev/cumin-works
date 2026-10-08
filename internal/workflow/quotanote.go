package workflow

// This file holds the sentence of the notification of "stop agent starts".
// It is pure: quota.go sends the notification.

import (
	"fmt"

	"github.com/cloveclovedev/cumin-works/internal/quota"
)

func limitReason(window quota.Name) string {
	if window == quota.Weekly {
		return "The weekly quota window reached its pace limit. cumin starts no agent until the pace limit rises above the usage or the window resets. The agent runs that are going on end as usual."
	}
	return fmt.Sprintf("The %s quota window reached its limit. cumin starts no agent until the window resets, a time band with a higher limit starts, or the command cumin quota allow runs. The agent runs that are going on end as usual.", window)
}
