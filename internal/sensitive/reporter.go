package sensitive

import (
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/pkg/provider"
)

// Reporter redacts every value and error before passing the event on
// to Inner, so neither the terminal nor the run record sees a
// sensitive value. It forwards the optional engine.UndoReporter and
// engine.ComponentVarsReporter events when Inner takes them.
type Reporter struct {
	Inner    engine.Reporter
	Redactor *Redactor
}

// Wrap returns inner unchanged when r has nothing to redact.
func Wrap(inner engine.Reporter, r *Redactor) engine.Reporter {
	if r == nil {
		return inner
	}
	return &Reporter{Inner: inner, Redactor: r}
}

// RunStarted implements engine.Reporter.
func (s *Reporter) RunStarted(scenario string) { s.Inner.RunStarted(scenario) }

// NodeStarted implements engine.Reporter.
func (s *Reporter) NodeStarted(nodeID string) { s.Inner.NodeStarted(nodeID) }

// NodeCompleted implements engine.Reporter.
func (s *Reporter) NodeCompleted(nodeID string, outputs map[string]cty.Value, d time.Duration) {
	s.Inner.NodeCompleted(nodeID, s.Redactor.Values(outputs), d)
}

// NodeFailed implements engine.Reporter.
func (s *Reporter) NodeFailed(nodeID string, err error) {
	s.Inner.NodeFailed(nodeID, s.Redactor.Error(err))
}

// RunCompleted implements engine.Reporter.
func (s *Reporter) RunCompleted(summary engine.RunSummary) {
	summary.Outputs = s.Redactor.Values(summary.Outputs)
	summary.Err = s.Redactor.Error(summary.Err)
	s.Inner.RunCompleted(summary)
}

// NodeUndo implements engine.UndoReporter.
func (s *Reporter) NodeUndo(nodeID string, undo provider.Invocation) {
	if u, ok := s.Inner.(engine.UndoReporter); ok {
		undo.Attrs = s.Redactor.Values(undo.Attrs)
		u.NodeUndo(nodeID, undo)
	}
}

// ComponentVars implements engine.ComponentVarsReporter.
func (s *Reporter) ComponentVars(componentKey string, vars map[string]cty.Value) {
	if c, ok := s.Inner.(engine.ComponentVarsReporter); ok {
		c.ComponentVars(componentKey, s.Redactor.Values(vars))
	}
}
