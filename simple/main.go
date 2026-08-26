package main

import (
	om "github.com/jom-io/gorig-om/src"
	"github.com/jom-io/gorig/bootstrap"
)

func main() {
	om.Setup()
	bootstrap.StartUp()
}
