package assert

import (
	"log"
)

func Assert(cond bool, msg string) {
	if !cond {
		log.Panicf("Assertion Failed: %s", msg)
	}
}

func AssertNotNil(target any, msg string) {
	Assert(target != nil, msg)
}

func AssertNil(target any, msg string) {
	Assert(target == nil, msg)
}
