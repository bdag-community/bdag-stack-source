package synch

type ProcessResultAction int

const (
	ProcessResultActionNull ProcessResultAction = iota
	ProcessResultActionContinue
	ProcessResultActionTryAgain
)

func (a ProcessResultAction) String() string {
	switch a {
	case ProcessResultActionNull:
		return "ProcessResultActionNull"
	case ProcessResultActionContinue:
		return "ProcessResultActionContinue"
	case ProcessResultActionTryAgain:
		return "ProcessResultActionTryAgain"
	}
	return ""
}

func (a ProcessResultAction) IsNothing() bool {
	return a == ProcessResultActionNull
}

func (a ProcessResultAction) IsContinue() bool {
	return a == ProcessResultActionContinue
}

func (a ProcessResultAction) IsTryAgain() bool {
	return a == ProcessResultActionTryAgain
}

type ProcessResult struct {
	act    ProcessResultAction
	orphan bool
	add    int
	reason string
	block  string
	err    error
}

func NewProcessResult() *ProcessResult {
	return &ProcessResult{act: ProcessResultActionNull, orphan: false}
}

func processResult(action ProcessResultAction, reason string, orphan bool, add int) *ProcessResult {
	return &ProcessResult{act: action, reason: reason, orphan: orphan, add: add}
}

func (r *ProcessResult) withBlock(block string) *ProcessResult {
	if r != nil {
		r.block = block
	}
	return r
}

func (r *ProcessResult) withErr(err error) *ProcessResult {
	if r != nil {
		r.err = err
	}
	return r
}

func (r *ProcessResult) reasonOr(fallback string) string {
	if r == nil || r.reason == "" {
		return fallback
	}
	return r.reason
}

func (r *ProcessResult) errString() string {
	if r == nil || r.err == nil {
		return ""
	}
	return r.err.Error()
}
