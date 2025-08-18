package errors

type ErrorAndStatus struct {
	StatusCode int
	ErrorMessage     string
}

func NewErrorAndStatus(statusCode int, message string) *ErrorAndStatus {
	return &ErrorAndStatus{
		StatusCode: statusCode,
		ErrorMessage: message,
	}
}