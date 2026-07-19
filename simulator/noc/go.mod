module gitlab.com/akita/noc

require (
	github.com/golang/mock v1.4.4
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/tebeka/atexit v0.3.0
	gitlab.com/akita/akita v1.10.1
	gitlab.com/akita/mem v1.8.7
	gitlab.com/akita/util v0.6.1
	golang.org/x/text v0.3.8 // indirect
)

replace gitlab.com/akita/akita => ../akita

replace gitlab.com/akita/mem => ../mem

replace gitlab.com/akita/util => ../util

go 1.13
