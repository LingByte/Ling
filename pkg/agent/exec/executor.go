package exec

import (
	"context"
	"fmt"
	"time"

	"github.com/LingByte/Ling/pkg/agent/plan"
)

type Executor struct {
	Runner Runner
	Opts   Options
}

func (e *Executor) Run(ctx context.Context, p *plan.Plan) (*Result, error) {
	if e == nil || e.Runner == nil {
		return nil, ErrMissingRunner
	}
	if p == nil {
		return nil, ErrInvalidWorkflow
	}

	opts := e.Opts
	if opts.MaxTasks <= 0 {
		opts.MaxTasks = 64
	}
	if len(p.Tasks) > opts.MaxTasks {
		return nil, fmt.Errorf("too many tasks: %d > %d", len(p.Tasks), opts.MaxTasks)
	}

	ordered, err := topoOrder(p.Tasks)
	if err != nil {
		return nil, err
	}

	st := State{Goal: p.Goal, Outputs: map[string]string{}, Artifacts: map[string]any{}}
	res := &Result{Goal: p.Goal, Final: st}

	for _, t := range ordered {
		tr := TaskResult{TaskID: t.ID, Status: TaskRunning, Started: time.Now()}
		out, runErr := e.Runner.RunTask(ctx, t, &st)
		tr.Finished = time.Now()
		tr.Latency = tr.Finished.Sub(tr.Started)
		if runErr != nil {
			tr.Status = TaskFailed
			tr.Error = runErr.Error()
			res.TaskResults = append(res.TaskResults, tr)
			res.Final = st
			if opts.StopOnError {
				return res, runErr
			}
			continue
		}
		tr.Status = TaskSucceeded
		tr.Output = out
		st.Outputs[t.ID] = out
		res.TaskResults = append(res.TaskResults, tr)
		res.Final = st
	}

	return res, nil
}
