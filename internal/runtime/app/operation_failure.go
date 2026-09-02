package app

import "github.com/fwtllh-png/QCode/internal/runtime/protocol"

func (r *Runtime) rejectResumableOperation(
	operation protocol.Operation,
	err error,
	release func(),
) bool {
	disposition := protocol.DispositionOf(err)
	if disposition != protocol.FaultRetryStep &&
		disposition != protocol.FaultResumeTurn {
		return false
	}
	release()
	if rejectErr := r.reject(operation, err); rejectErr == nil {
		r.commit(operation.ID)
	}
	return true
}

func (r *OperationService) reject(
	operation protocol.Operation,
	err error,
) error {
	problem := protocol.ProblemOf(err)
	return (&runtimeSink{runtime: r.Runtime, operation: operation}).Emit(
		&protocol.OperationRejectedData{
			Code: problem.Code, Message: problem.Message,
			Fault: problem.Fault,
		},
	)
}
