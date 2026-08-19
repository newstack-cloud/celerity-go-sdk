// Package manifest produces the handler-manifest.v1 document the Celerity CLI
// discovers an application's handlers from.
//
// The document is generated from the same registry the dispatcher routes
// against, so a handler that appears in it is by construction one the binary
// will serve. That is the property static analysis of a Go program cannot
// offer, and it is why extraction runs the binary.
//
// What running cannot answer is which resources each handler reaches: that
// dependency is captured in a closure, and Go offers no reflection into one. It
// is filled in by the extraction tool's static pass over the same source.
package manifest

// Version is the manifest format this package emits.
const Version = "v1"

// Manifest is the document the CLI reads.
//
// Go populates FunctionHandlers only: ClassHandlers exists for the Node and
// Python SDKs, whose decorators sit on classes.
type Manifest struct {
	Version          string            `json:"version"`
	Handlers         []ClassHandler    `json:"handlers"`
	FunctionHandlers []FunctionHandler `json:"functionHandlers"`
	GuardHandlers    []GuardHandler    `json:"guardHandlers"`

	// LinkedAdapters and LinkedResourceProviders name the platform support
	// compiled into the binary.
	//
	// Go links one artefact, so a deployment targeting a platform the binary
	// carries no adapter for cannot be fixed by configuration. Reporting what is
	// linked lets the CLI refuse that at build time, naming the import to add,
	// rather than leaving it to the first invocation. Additive fields: an older
	// CLI ignores them.
	LinkedAdapters          []string `json:"linkedAdapters,omitempty"`
	LinkedResourceProviders []string `json:"linkedResourceProviders,omitempty"`
}

// ClassHandler is always empty for Go, and is present so the document shape
// matches what the CLI expects from every SDK.
type ClassHandler struct{}

// FunctionHandler describes one registered handler.
type FunctionHandler struct {
	ResourceName string         `json:"resourceName"`
	ExportName   string         `json:"exportName"`
	SourceFile   string         `json:"sourceFile"`
	Annotations  map[string]any `json:"annotations,omitempty"`
	Spec         Spec           `json:"spec"`

	// FuncPath is the fully qualified name of the registered function, such as
	// github.com/acme/app/orders.Create. The extraction tool's static pass roots
	// its resource walk here, which is what ties the analysis to the function
	// the binary will actually dispatch to. Not part of the CLI's contract.
	FuncPath string `json:"-"`
}

// GuardHandler describes one registered auth guard.
type GuardHandler struct {
	ResourceName string         `json:"resourceName"`
	GuardName    string         `json:"guardName"`
	SourceFile   string         `json:"sourceFile"`
	GuardType    string         `json:"guardType"`
	ExportName   string         `json:"exportName,omitempty"`
	Annotations  map[string]any `json:"annotations"`
	Spec         Spec           `json:"spec"`
}

// Spec holds the blueprint spec fields for a handler resource.
type Spec struct {
	HandlerName  string `json:"handlerName"`
	CodeLocation string `json:"codeLocation"`
	Handler      string `json:"handler"`
	Timeout      *int   `json:"timeout,omitempty"`
}

// Annotation keys, matching what the Node and Python SDKs emit so that the
// CLI's blueprint transform needs no per-language handling.
const (
	AnnotationHTTP             = "celerity.handler.http"
	AnnotationHTTPMethod       = "celerity.handler.http.method"
	AnnotationHTTPPath         = "celerity.handler.http.path"
	AnnotationWebSocket        = "celerity.handler.websocket"
	AnnotationWebSocketRoute   = "celerity.handler.websocket.route"
	AnnotationConsumer         = "celerity.handler.consumer"
	AnnotationConsumerSource   = "celerity.handler.consumer.source"
	AnnotationSchedule         = "celerity.handler.schedule"
	AnnotationTag              = "celerity.handler.tag"
	AnnotationGuardProtectedBy = "celerity.handler.guard.protectedBy"
	AnnotationPublic           = "celerity.handler.public"
	// AnnotationResourceRef carries the blueprint resources a handler reaches,
	// which is what the CLI turns into IAM grants, environment wiring and
	// security group rules.
	AnnotationResourceRef = "celerity.handler.resource.ref"
)
