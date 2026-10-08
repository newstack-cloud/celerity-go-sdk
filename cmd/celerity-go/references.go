package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Which resources a handler reaches is the one thing running the binary cannot
// answer, the handle is captured in a closure or held on a receiver, and Go
// offers no reflection into either. So it is resolved from the source, rooted at
// the function the binary registered.
//
// What the walk looks for is not calls to a resource, but references to the
// handle a resource call produced. A handler does not typically call
// resources.Bucket(app, "uploads"); it closes over the value that call
// returned, or reaches it through its receiver, and calls Get on that.

// The handle constructors, by the kind each answers with.
var resourceKinds = map[string]string{
	"Bucket":      "bucket",
	"Queue":       "queue",
	"Topic":       "topic",
	"Cache":       "cache",
	"Datastore":   "datastore",
	"SQLDatabase": "sqlDatabase",
}

// Where the handle constructors live.
const resourcesPackage = "github.com/newstack-cloud/celerity-go-sdk/resources"

// What a handle taken without a name refers to, and
// matches resources.DefaultName.
const defaultResourceName = "default"

// What the runtime calls the package an application's entry
// point is in, whatever its import path is.
const mainPackage = "main"

// The resources each handler reaches, by the name the runtime
// knows the handler as.
type references map[string][]string

// Walks an application's source to answer what each handler reaches.
type resolver struct {
	loaded map[string]*packages.Package
	// handles is every object holding a resource handle including a variable a
	// constructor was assigned to, a field one was stored in, or a parameter
	// one arrives as.
	//
	// A set per object rather than one ref, because an object is reached from
	// more than one place: a field assigned a different bucket by two
	// constructions holds either, and a parameter is whatever its callers pass.
	// Keeping the last would drop a resource, which is a failure that
	// needs to be avoided here, so every one of them is kept.
	handles map[types.Object]map[string]bool
	// bodies is every function declared in the loaded packages, so that a call
	// from a handler into one can be followed.
	bodies map[types.Object]*ast.FuncDecl
	// aliases is a func-typed variable holding a function itself, as in
	// factory := newSender. Calling the variable is calling that function, so
	// its parameters bind to the arguments and its body is what to walk.
	aliases map[types.Object]*types.Func
	// produced is a func-typed variable holding what calling a function
	// returned, as in publish := newPublisher(events). Calling the variable
	// runs a literal declared inside that function, so its body is what to
	// walk, but the arguments are the literal's rather than the factory's and
	// bind to nothing here.
	produced map[types.Object]*types.Func
	// info is the type information for the file a node came from.
	info map[*ast.File]*packages.Package
	// resolving is the functions whose returns are being read, so that one
	// returning a call to itself is not followed forever.
	resolving map[types.Object]bool
}

// Loads the packages rooted at dir and answers what each of
// the named handlers reaches.
func resolveReferences(dir string, handlers []string) (references, error) {
	loaded, err := packages.Load(
		&packages.Config{
			Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes |
				packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
			Dir: dir,
		},
		"./...",
	)
	if err != nil {
		return nil, fmt.Errorf("loading the application's packages: %w", err)
	}

	if err := loadErrors(loaded); err != nil {
		// Reported rather than treated as an application that reaches nothing,
		// which is the silent failure worth guarding against. Every handler
		// would be granted access to nothing and fail on its first event.
		return nil, err
	}

	r := newResolver(loaded)
	if err := r.collectHandles(); err != nil {
		return nil, err
	}

	resolved := references{}
	for _, handler := range handlers {
		names, err := r.reach(handler)
		if err != nil {
			return nil, err
		}

		if len(names) > 0 {
			resolved[handler] = names
		}
	}

	return resolved, nil
}

func loadErrors(pkgs []*packages.Package) error {
	var failures []string
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		for _, err := range pkg.Errors {
			failures = append(failures, err.Error())
		}
	})
	if len(failures) == 0 {
		return nil
	}

	return fmt.Errorf(
		"reading the application's source to resolve what each handler reaches:\n\t%s",
		strings.Join(failures, "\n\t"))
}

func newResolver(pkgs []*packages.Package) *resolver {
	r := &resolver{
		loaded:    map[string]*packages.Package{},
		handles:   map[types.Object]map[string]bool{},
		bodies:    map[types.Object]*ast.FuncDecl{},
		aliases:   map[types.Object]*types.Func{},
		produced:  map[types.Object]*types.Func{},
		info:      map[*ast.File]*packages.Package{},
		resolving: map[types.Object]bool{},
	}
	for _, pkg := range pkgs {
		r.loaded[pkg.PkgPath] = pkg
		if pkg.Name == mainPackage {
			// The runtime names a function in the main package main.Something
			// whatever the module is called, so that is the name a handler
			// registered there is looked up by.
			r.loaded[mainPackage] = pkg
		}
		for _, file := range pkg.Syntax {
			r.info[file] = pkg
		}
	}
	return r
}

// Finds every object a resource handle was put into, and every
// function body a call could be followed into.
func (r *resolver) collectHandles() error {
	for {
		before := r.refCount()
		for _, pkg := range r.loaded {
			for _, file := range pkg.Syntax {
				if err := r.collectFile(pkg, file); err != nil {
					return err
				}
			}
		}
		if r.refCount() == before {
			// No more references have been collected in the latest
			// iteration, so there's no need to continue collecting.
			return nil
		}
	}
}

func (r *resolver) refCount() int {
	// The indirections are included too, learning which function is behind a variable
	// is what lets the next pass bind its parameters, so a pass that found only
	// those has still made progress.
	total := len(r.aliases) + len(r.produced)
	for _, refs := range r.handles {
		total += len(refs)
	}
	return total
}

// Records that an object holds a resource.
func (r *resolver) note(obj types.Object, refs map[string]bool) {
	if obj == nil || len(refs) == 0 {
		return
	}

	held, known := r.handles[obj]
	if !known {
		held = map[string]bool{}
		r.handles[obj] = held
	}

	for ref := range refs {
		held[ref] = true
	}
}

func (r *resolver) collectFile(pkg *packages.Package, file *ast.File) error {
	var failure error
	ast.Inspect(file, func(node ast.Node) bool {
		if failure != nil {
			return false
		}

		switch n := node.(type) {
		case *ast.FuncDecl:
			if obj := pkg.TypesInfo.Defs[n.Name]; obj != nil && n.Body != nil {
				r.bodies[obj] = n
			}
		case *ast.AssignStmt:
			failure = r.collectAssignment(pkg, n.Lhs, n.Rhs)
		case *ast.ValueSpec:
			failure = r.collectAssignment(pkg, identsAsExprs(n.Names), n.Values)
		case *ast.CompositeLit:
			failure = r.collectLiteral(pkg, n)
		case *ast.CallExpr:
			failure = r.collectCall(pkg, n)
		}
		return true
	})
	return failure
}

// Maps what a resource constructor was assigned to.
func (r *resolver) collectAssignment(
	pkg *packages.Package, left, right []ast.Expr,
) error {
	// A constructor that can fail answers two values, which is one expression
	// on the right and two names on the left: store, err := newRepo(items).
	// Skipping those would lose the only thing connecting the variable to what
	// the constructor produced, and a constructor that reaches out to establish
	// a connection or obtain credentials is the ordinary reason for one.
	if len(left) > 1 && len(right) == 1 {
		return r.collectResults(pkg, left, right[0])
	}

	// Anything else uneven is not an assignment this can read.
	if len(left) != len(right) {
		return nil
	}

	for i, value := range right {
		refs, err := r.held(pkg, value)
		if err != nil {
			return err
		}

		r.note(objectOf(pkg, left[i]), refs)
		r.noteFunction(pkg, left[i], value)
	}
	return nil
}

// Records the function behind a func-typed variable.
//
// A factory is not always named at the call site, it can be assigned to a variable
// and called through it, or it answers with a closure that is called through
// one. Either way the call names a variable rather than a declaration, and
// without resolving it the walk stops and the dependency would not be captured.
func (r *resolver) noteFunction(pkg *packages.Package, target, value ast.Expr) {
	obj := objectOf(pkg, target)
	if obj == nil || !isFunction(obj.Type()) {
		return
	}

	if call, isCall := value.(*ast.CallExpr); isCall {
		if fn, isFunc := r.calledFunc(pkg, call); isFunc {
			r.produced[obj] = fn
		}
		return
	}

	if fn, isFunc := referenced(pkg, value).(*types.Func); isFunc {
		r.aliases[obj] = fn
	}
}

// Reports whether a type is a function, which is what a variable
// holding a factory or a closure has.
func isFunction(typ types.Type) bool {
	if typ == nil {
		return false
	}

	_, isSignature := typ.Underlying().(*types.Signature)
	return isSignature
}

// behind is the function a call through a variable reaches, and nil for a call
// this cannot resolve.
func (r *resolver) behind(obj types.Object) *types.Func {
	if fn, aliased := r.aliases[obj]; aliased {
		return fn
	}

	return r.produced[obj]
}

// Maps a constructor stored straight into a struct field, which
// is how a handler reaching one through its receiver gets it.
func (r *resolver) collectLiteral(pkg *packages.Package, literal *ast.CompositeLit) error {
	for _, element := range literal.Elts {
		pair, isPair := element.(*ast.KeyValueExpr)
		if !isPair {
			continue
		}

		refs, err := r.held(pkg, pair.Value)
		if err != nil {
			return err
		}

		if key, isIdent := pair.Key.(*ast.Ident); isIdent {
			r.note(pkg.TypesInfo.Uses[key], refs)
		}
	}

	return nil
}

// Maps a handle passed as an argument to the parameter it arrives
// as.
//
// This is how a handle reaches a field that is only ever assigned inside a
// constructor. For example, newShipping(outbox) stores its parameter,
// not the variable the caller named, so without this the field
// holds nothing as far as the walk can tell and the handler reports no queue.
// The fixed point then carries it on,
// since the parameter being known makes the composite literal inside resolve on
// the next pass.
//
// A parameter is whatever its callers pass, and they may pass different
// resources that need to be captured.
func (r *resolver) collectCall(pkg *packages.Package, call *ast.CallExpr) error {
	fn, isFunc := r.calledFunc(pkg, call)
	if !isFunc {
		return nil
	}

	signature, isSignature := fn.Type().(*types.Signature)
	if !isSignature {
		return nil
	}

	// The variadic slot takes a slice of what was passed rather than one
	// argument, so only the positions before it line up.
	fixed := signature.Params().Len()
	if signature.Variadic() {
		fixed--
	}

	for i := 0; i < fixed && i < len(call.Args); i++ {
		refs, err := r.held(pkg, call.Args[i])
		if err != nil {
			return err
		}

		r.note(signature.Params().At(i), refs)
	}
	return nil
}

// The function a call names, resolving a variable that holds one.
//
// Only an alias, not a variable holding what a factory returned: the arguments
// of a call through one of those belong to the closure rather than to the
// factory, and binding them to the factory's parameters would record a resource
// the factory never saw.
func (r *resolver) calledFunc(pkg *packages.Package, call *ast.CallExpr) (*types.Func, bool) {
	var obj types.Object
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		obj = pkg.TypesInfo.Uses[fn]
	case *ast.SelectorExpr:
		obj = pkg.TypesInfo.Uses[fn.Sel]
	default:
		return nil, false
	}

	if fn, isFunc := obj.(*types.Func); isFunc {
		return fn, true
	}

	fn, aliased := r.aliases[obj]
	return fn, aliased
}

// Reports which resources an expression holds, whether it is the
// constructor call itself or something already known to hold what one answered.
func (r *resolver) held(pkg *packages.Package, expr ast.Expr) (map[string]bool, error) {
	kind, name, constructed, err := r.constructed(pkg, expr)
	if err != nil {
		return nil, err
	}

	if constructed {
		return map[string]bool{kind + "/" + name: true}, nil
	}

	if obj := referenced(pkg, expr); obj != nil {
		return r.handles[obj], nil
	}

	if call, isCall := expr.(*ast.CallExpr); isCall {
		return r.returned(pkg, call)
	}

	return nil, nil
}

// Maps every name on the left of an assignment from one call.
//
// Which of them received the resource is not something the expression says, so
// all of them are read from it.
func (r *resolver) collectResults(
	pkg *packages.Package, left []ast.Expr, value ast.Expr,
) error {
	refs, err := r.held(pkg, value)
	if err != nil {
		return err
	}

	for _, target := range left {
		r.note(objectOf(pkg, target), refs)
		r.noteFunction(pkg, target, value)
	}
	return nil
}

// returned is what a call answers with, read from what the function returns.
//
// A constructor with work to do before it can hand back a resource answers with
// one: openArchive(store) establishes a connection and returns the client, and
// nothing but its return statement connects the two. The parameters are bound
// by the time this is read, so returning one of them resolves, and the fixed
// point carries it to whatever the result was assigned to.
//
// Guarded against a function that returns a call to itself, which would
// otherwise recur until the stack ran out.
func (r *resolver) returned(pkg *packages.Package, call *ast.CallExpr) (map[string]bool, error) {
	fn, isFunc := r.calledFunc(pkg, call)
	if !isFunc || r.resolving[fn] {
		return nil, nil
	}

	declaration, declared := r.bodies[fn]
	if !declared {
		return nil, nil
	}

	r.resolving[fn] = true
	defer delete(r.resolving, fn)

	within := r.packageOf(declaration)
	if within == nil {
		return nil, nil
	}

	refs := map[string]bool{}
	for _, result := range returnedExprs(declaration.Body) {
		answered, err := r.held(within, result)
		if err != nil {
			return nil, err
		}

		for ref := range answered {
			refs[ref] = true
		}
	}
	return refs, nil
}

// The expressions a body returns, the function's own rather
// than those of a literal declared inside it.
func returnedExprs(body *ast.BlockStmt) []ast.Expr {
	var out []ast.Expr
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncLit:
			// A literal's returns are its own, and it is reached by following
			// the call rather than by reading what encloses it.
			return false
		case *ast.ReturnStmt:
			out = append(out, n.Results...)
		}
		return true
	})
	return out
}

// The object an expression reads, and nil for anything else.
func referenced(pkg *packages.Package, expr ast.Expr) types.Object {
	switch e := expr.(type) {
	case *ast.Ident:
		return pkg.TypesInfo.Uses[e]
	case *ast.SelectorExpr:
		return pkg.TypesInfo.Uses[e.Sel]
	default:
		return nil
	}
}

// Reports whether an expression is a resource constructor call,
// and which resource it answers with.
func (r *resolver) constructed(
	pkg *packages.Package, expr ast.Expr,
) (kind, name string, held bool, err error) {
	call, isCall := expr.(*ast.CallExpr)
	if !isCall {
		return "", "", false, nil
	}

	selector, isSelector := call.Fun.(*ast.SelectorExpr)
	if !isSelector {
		return "", "", false, nil
	}

	fn, isFunc := pkg.TypesInfo.Uses[selector.Sel].(*types.Func)
	if !isFunc || fn.Pkg() == nil || fn.Pkg().Path() != resourcesPackage {
		return "", "", false, nil
	}
	kind, known := resourceKinds[fn.Name()]
	if !known {
		return "", "", false, nil
	}

	// The name is variadic, so a handle taken without one refers to the only
	// resource of its kind, which the CLI resolves against the blueprint.
	if len(call.Args) < 2 {
		return kind, defaultResourceName, true, nil
	}

	value := pkg.TypesInfo.Types[call.Args[1]].Value
	if value == nil || value.Kind() != constant.String {
		return "", "", false, fmt.Errorf(
			"%s: the name given to resources.%s is not a constant, so extraction "+
				"cannot tell which resource it is.\nName it with a literal or a "+
				"constant, or declare it with celerity.Uses on the handler",
			pkg.Fset.Position(call.Pos()), fn.Name())
	}

	return kind, constant.StringVal(value), true, nil
}

// reach walks a handler and answers the resources it reaches, following calls
// into the application's own functions.
func (r *resolver) reach(handler string) ([]string, error) {
	path, err := parseFuncPath(handler)
	if err != nil {
		// A name the runtime produced that this cannot read is worth reporting
		// rather than treating as a handler that reaches nothing.
		return nil, fmt.Errorf("reading the handler name %q: %w", handler, err)
	}

	_, body, found := bodyOf(r.loaded, path)
	if !found {
		return nil, nil
	}

	reached := map[string]bool{}
	r.walk(body, reached, map[types.Object]bool{})

	names := make([]string, 0, len(reached))
	for name := range reached {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Collects the handles a body references, following calls it makes.
func (r *resolver) walk(body ast.Node, found map[string]bool, visited map[types.Object]bool) {
	pkg := r.packageOf(body)
	if pkg == nil {
		return
	}

	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Ident:
			// A handle closed over, which is the typical case.
			r.record(pkg.TypesInfo.Uses[n], found)
		case *ast.SelectorExpr:
			// A handle on a receiver, or on something the handler was given.
			r.record(pkg.TypesInfo.Uses[n.Sel], found)
		case *ast.CallExpr:
			r.follow(pkg, n, found, visited)
		}
		return true
	})
}

func (r *resolver) record(obj types.Object, found map[string]bool) {
	if obj == nil {
		return
	}

	for ref := range r.handles[obj] {
		found[strings.SplitN(ref, "/", 2)[1]] = true
	}
}

// follow walks into a function the handler calls, so that a handler whose work
// is done by a service it holds reports what that service reaches.
func (r *resolver) follow(
	pkg *packages.Package, call *ast.CallExpr, found map[string]bool, visited map[types.Object]bool,
) {
	var called types.Object
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		called = pkg.TypesInfo.Uses[fn]
	case *ast.SelectorExpr:
		called = pkg.TypesInfo.Uses[fn.Sel]
	}
	if called == nil || visited[called] {
		return
	}

	visited[called] = true

	if declaration, declared := r.bodies[called]; declared {
		r.walk(declaration.Body, found, visited)
		return
	}

	// A call through a variable: the function behind it is what to walk, and
	// for a factory that answered with a closure the literal is in that body.
	if behind := r.behind(called); behind != nil && !visited[behind] {
		visited[behind] = true
		if declaration, declared := r.bodies[behind]; declared {
			r.walk(declaration.Body, found, visited)
			return
		}
	}

	// An interface method does not name a body. What can be behind it are the
	// application's own types, so those are walked instead; anything else is a
	// call into a dependency, which is not this application's source.
	r.followInterface(called, found, visited)
}

// Walks what the application's own types do for an interface
// method.
//
// A service held behind an interface is ordinary Go, and the call site names
// the interface rather than whatever was stored in it. Resolving which one it
// was needs to know what reached that field, so instead, every implementation
// the application declares is walked and the handler reports the union.
//
// This over-reports where an interface has more than one implementation,
// which is the safe approach as all the extraction really cares about
// are Celerity resources in the dependency tree.
func (r *resolver) followInterface(
	method types.Object, found map[string]bool, visited map[types.Object]bool,
) {
	fn, isFunc := method.(*types.Func)
	if !isFunc || fn.Pkg() == nil {
		return
	}

	signature, isSignature := fn.Type().(*types.Signature)
	if !isSignature || signature.Recv() == nil {
		return
	}

	declared, isInterface := signature.Recv().Type().Underlying().(*types.Interface)
	if !isInterface {
		return
	}

	for _, implementation := range r.implementations(declared, fn) {
		if visited[implementation] {
			continue
		}
		visited[implementation] = true
		if body, known := r.bodies[implementation]; known {
			r.walk(body.Body, found, visited)
		}
	}
}

// implementations are the methods on the application's own types that satisfy
// an interface method.
func (r *resolver) implementations(declared *types.Interface, fn *types.Func) []types.Object {
	var out []types.Object
	for _, pkg := range r.loaded {
		if pkg.Types == nil {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			out = append(out, satisfying(scope.Lookup(name), declared, fn)...)
		}
	}

	return out
}

// satisfying is the method an object's type contributes to an interface, for a
// named type that is not itself an interface.
//
// Both the value and the pointer are considered, since a method on a pointer
// receiver puts the interface in the pointer's method set alone and a service
// is commonly held as one.
func satisfying(obj types.Object, declared *types.Interface, fn *types.Func) []types.Object {
	typeName, isType := obj.(*types.TypeName)
	if !isType {
		return nil
	}

	named, isNamed := typeName.Type().(*types.Named)
	if !isNamed || types.IsInterface(named) {
		return nil
	}

	var out []types.Object
	for _, candidate := range []types.Type{named, types.NewPointer(named)} {
		if !types.Implements(candidate, declared) {
			continue
		}

		method, _, _ := types.LookupFieldOrMethod(candidate, true, fn.Pkg(), fn.Name())
		if method != nil {
			out = append(out, method)
		}
	}

	return out
}

// Finds the type information for the file a node is in.
func (r *resolver) packageOf(node ast.Node) *packages.Package {
	for file, pkg := range r.info {
		if file.Pos() <= node.Pos() && node.Pos() <= file.End() {
			return pkg
		}
	}
	return nil
}

func objectOf(pkg *packages.Package, expr ast.Expr) types.Object {
	ident, isIdent := expr.(*ast.Ident)
	if !isIdent {
		if selector, isSelector := expr.(*ast.SelectorExpr); isSelector {
			return pkg.TypesInfo.Uses[selector.Sel]
		}
		return nil
	}

	if obj := pkg.TypesInfo.Defs[ident]; obj != nil {
		return obj
	}

	return pkg.TypesInfo.Uses[ident]
}

func identsAsExprs(idents []*ast.Ident) []ast.Expr {
	exprs := make([]ast.Expr, 0, len(idents))
	for _, ident := range idents {
		exprs = append(exprs, ident)
	}

	return exprs
}
