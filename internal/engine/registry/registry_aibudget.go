package registry

import (
	"github.com/gsoultan/hermod/internal/aibudget"
)

// AIBudget is the vhosts' AI spending policy over whatever storage the
// registry holds right now: its store when it has a database, the control
// plane's API when it is a worker. It is built once, since it caches each
// vhost's budget, and cmd/hermod installs it with genai.SetBudget so that
// every AI node and the agent are held to it.
func (r *Registry) AIBudget() *aibudget.Service {
	r.aiBudgetOnce.Do(func() {
		var notify aibudget.Notifier
		if r.notificationService != nil {
			notify = r.notificationService
		}
		r.aiBudget = aibudget.NewService(func() any { return r.store() }, notify, registryLogger{r})
	})
	return r.aiBudget
}

// registryLogger forwards to the registry's logger as it is at the time of
// each line, since SetLogger can replace it after the budget is built.
type registryLogger struct{ r *Registry }

func (l registryLogger) Debug(msg string, kv ...any) { l.r.Logger().Debug(msg, kv...) }
func (l registryLogger) Info(msg string, kv ...any)  { l.r.Logger().Info(msg, kv...) }
func (l registryLogger) Warn(msg string, kv ...any)  { l.r.Logger().Warn(msg, kv...) }
func (l registryLogger) Error(msg string, kv ...any) { l.r.Logger().Error(msg, kv...) }
