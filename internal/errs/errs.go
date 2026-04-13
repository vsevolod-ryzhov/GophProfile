package errs

import "errors"

var (
	UserIDHeaderNotFound             = errors.New("X-User-ID header is required")
	UserIDHeaderExceedsMaximumLength = errors.New("X-User-ID exceeds maximum length")
	UserIDHeaderContainsInvalidChar  = errors.New("X-User-ID contains invalid characters")
	ExpectedMultipartFormData        = errors.New("expected multipart form data")
	FileTooLarge                     = errors.New("file too large")
	InvalidMultipartBody             = errors.New("invalid multipart body")
	MissingFileField                 = errors.New("missing file field")
	EmptyFile                        = errors.New("file is empty")
	FileReadError                    = errors.New("file read error")
	UnsupportedMediaType             = errors.New("unsupported media type")
	FileSizeMismatch                 = errors.New("file size mismatch")
	InternalError                    = errors.New("internal server error")
)
