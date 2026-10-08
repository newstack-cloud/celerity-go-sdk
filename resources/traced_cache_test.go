package resources_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/celeritytest"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

type TracedCacheTestSuite struct {
	suite.Suite
}

func TestTracedCacheTestSuite(t *testing.T) {
	suite.Run(t, new(TracedCacheTestSuite))
}

func (s *TracedCacheTestSuite) Test_every_operation_is_traced_as_itself() {
	// Every operation on the contract is called and the one underneath asked
	// what it saw, so an operation added to the contract is covered the day it
	// is added rather than when somebody remembers to write a case for it.
	contract := reflect.TypeOf((*cache.Client)(nil)).Elem()
	s.Require().Greater(contract.NumMethod(), 40, "the whole contract, not a few of it")

	for i := range contract.NumMethod() {
		operation := contract.Method(i)

		s.Run(operation.Name, func() {
			traced, stub := s.tracedCache()
			tracer := celeritytest.NewTracer(s.T())

			s.call(traced, operation)

			s.Require().True(stub.Called(operation.Name),
				"the operation underneath should be the same one: saw %v", stub.Calls())
			s.Require().Contains(tracer.Names(), "celerity.cache."+snakeCase(operation.Name),
				"the span should name the operation it wraps")
		})
	}
}

// tracedCache returns what an application is given for a cache, which is the
// tracing wrapper rather than the double, and the double underneath it.
//
// Taken the way an application takes it, so the wrapper under test is the one a
// handler would actually hold.
func (s *TracedCacheTestSuite) tracedCache() (cache.Client, *celeritytest.CacheStub) {
	s.T().Helper()

	res := celeritytest.Resources()
	stub := res.CacheNamed("sessions")

	app := celerity.New(celerity.WithResourceProvider(res))
	client := resources.Cache(app, "sessions")
	s.Require().NoError(app.Err())

	return client, stub
}

// call invokes one operation with zero values, which is enough: what is being
// asserted is which operation was reached, not what it did with its arguments.
func (s *TracedCacheTestSuite) call(client cache.Client, operation reflect.Method) {
	s.T().Helper()

	method := reflect.ValueOf(client).MethodByName(operation.Name)
	signature := method.Type()

	args := make([]reflect.Value, 0, signature.NumIn())
	for i := range signature.NumIn() {
		if signature.IsVariadic() && i == signature.NumIn()-1 {
			break
		}
		if i == 0 {
			// A nil context would panic before the operation was reached.
			args = append(args, reflect.ValueOf(context.Background()))
			continue
		}
		args = append(args, reflect.Zero(signature.In(i)))
	}

	// A walk yields rather than returning, so reading it is what runs it.
	for _, result := range method.Call(args) {
		if result.Kind() == reflect.Func {
			drain(result)
		}
	}
}

// drain reads an iterator far enough for the operation to have run.
func drain(sequence reflect.Value) {
	yield := reflect.MakeFunc(
		sequence.Type().In(0),
		func([]reflect.Value) []reflect.Value {
			return []reflect.Value{reflect.ValueOf(false)}
		})
	sequence.Call([]reflect.Value{yield})
}

// snakeCase is how an operation's name becomes a span's, with an acronym kept
// whole: TTL is ttl rather than t_t_l.
func snakeCase(name string) string {
	var out strings.Builder
	runes := []rune(name)

	for i, r := range runes {
		upper := r >= 'A' && r <= 'Z'
		previousUpper := i > 0 && runes[i-1] >= 'A' && runes[i-1] <= 'Z'
		if upper && i > 0 && !previousUpper {
			out.WriteByte('_')
		}
		out.WriteRune(r | 0x20)
	}
	return out.String()
}
