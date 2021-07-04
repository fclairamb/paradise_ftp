module github.com/fclairamb/ftpserverlib

go 1.16

require (
	github.com/go-kit/kit v0.11.0
	github.com/secsy/goftp v0.0.0-20200609142545-aa2de14babf4
	github.com/spf13/afero v1.6.0
	github.com/stretchr/testify v1.7.0
)

replace github.com/secsy/goftp => github.com/drakkan/goftp v0.0.0-20201220151643-27b7174e8caf
