package workflow_test

import (
	"context"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// pollAtMinute moves the clock to that many minutes after the start of the
// scene, and polls. It returns the error of the poll.
func pollAtMinute(sc *scene, service *workflow.Service, minutes int) error {
	sc.clock.Set(sceneNow.Add(time.Duration(minutes) * time.Minute))
	err := service.Poll(context.Background())
	service.Wait()
	return err
}
