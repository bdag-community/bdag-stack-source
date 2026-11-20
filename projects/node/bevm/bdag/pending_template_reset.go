package bdag

import "errors"

// PendingTemplateResetCode identifies the stable reason a BdagPool pending
// template reset failed.
type PendingTemplateResetCode string

const (
	PendingTemplateResetFailed  PendingTemplateResetCode = "failed"
	PendingTemplateResetStopped PendingTemplateResetCode = "stopped"
	PendingTemplateResetSyncing PendingTemplateResetCode = "syncing"
)

func (c PendingTemplateResetCode) String() string {
	return string(c)
}

// PendingTemplateResetError preserves the original error for errors.Is while
// giving callers a stable machine code for mining health classification.
type PendingTemplateResetError struct {
	Code PendingTemplateResetCode
	Err  error
}

func (e *PendingTemplateResetError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Code.String()
	}
	return e.Err.Error()
}

func (e *PendingTemplateResetError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func wrapPendingTemplateResetError(err error) error {
	if err == nil {
		return nil
	}
	var resetErr *PendingTemplateResetError
	if errors.As(err, &resetErr) {
		return err
	}
	return &PendingTemplateResetError{
		Code: classifyPendingTemplateResetError(err),
		Err:  err,
	}
}

func classifyPendingTemplateResetError(err error) PendingTemplateResetCode {
	switch {
	case errors.Is(err, errBdagPoolStopped):
		return PendingTemplateResetStopped
	case errors.Is(err, errBdagPoolSyncing):
		return PendingTemplateResetSyncing
	default:
		return PendingTemplateResetFailed
	}
}
