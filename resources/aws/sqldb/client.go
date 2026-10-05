package sqldb

import (
	"context"
	"errors"
	"sync"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
)

// Linking this package is what lets an application reach an RDS database.
func init() {
	service.RegisterSQLDatabase(func(s *service.Session) service.Builder[sqldb.Client] {
		return newDatabases(s).build
	})
	service.RegisterReleaser(release)
}

// databases builds database handles for one provider.
//
// There is no AWS service client here: a database is reached through
// database/sql with a driver the build linked, and the session is what an IAM
// connection signs a token with.
type databases struct {
	session *service.Session
}

func newDatabases(s *service.Session) *databases { return &databases{session: s} }

func (d *databases) build(ref resources.Ref) (sqldb.Client, error) {
	database := &rdsDatabase{databases: d, ref: ref}
	track(database)
	return database, nil
}

// The databases built, so that the pools they opened can be given back when the
// application is shut down. A handle is taken during registration and held for
// the life of the process, so nothing is ever removed from this: it is the list
// of what the application reaches, which is as long as the blueprint's.
var (
	builtMu sync.Mutex
	built   []*rdsDatabase
)

func track(database *rdsDatabase) {
	builtMu.Lock()
	defer builtMu.Unlock()
	built = append(built, database)
}

// release closes the pools every database opened.
//
// A pool that was never opened is skipped. A handle resolves on first use, so an
// application that declared a database and never queried it has nothing to give
// back.
func release(ctx context.Context) error {
	builtMu.Lock()
	databases := make([]*rdsDatabase, len(built))
	copy(databases, built)
	builtMu.Unlock()

	var errs []error
	for _, database := range databases {
		errs = append(errs, database.close(ctx))
	}
	return errors.Join(errs...)
}

type databaseClient = sqldb.Client
