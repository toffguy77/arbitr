package orderexec

type Executor interface{ Submit(plan any) error }

type StubExecutor struct{}

func (s StubExecutor) Submit(plan any) error { return nil }
