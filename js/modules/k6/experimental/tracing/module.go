// Package tracing implements a k6 JS module for instrumenting k6 scripts with tracing context information.
package tracing

import (
	"errors"
	"fmt"

	"github.com/dop251/goja"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/js/modules"
)

type (
	// RootModule is the global module instance that will create Client
	// instances for each VU.
	RootModule struct{}

	// ModuleInstance represents an instance of the JS module.
	ModuleInstance struct {
		vu modules.VU

		// Client holds the module's default tracing client.
		*Client
	}
)

// Ensure the interfaces are implemented correctly
var (
	_ modules.Instance = &ModuleInstance{}
	_ modules.Module   = &RootModule{}
)

// New returns a pointer to a new RootModule instance
func New() *RootModule {
	return &RootModule{}
}

// NewModuleInstance implements the modules.Module interface and returns
// a new instance for each VU.
func (*RootModule) NewModuleInstance(vu modules.VU) modules.Instance {
	return &ModuleInstance{
		vu: vu,
	}
}

// Exports implements the modules.Instance interface and returns
// the exports of the JS module.
func (mi *ModuleInstance) Exports() modules.Exports {
	return modules.Exports{
		Named: map[string]interface{}{
			"Client":         mi.newClient,
			"instrumentHTTP": mi.instrumentHTTP,
		},
	}
}

// NewClient is the JS constructor for the tracing.Client
//
// It expects a single configuration object as argument, which
// will be used to instantiate an `Object` instance internally,
// and will be used by the client to configure itself.
func (mi *ModuleInstance) newClient(cc goja.ConstructorCall) *goja.Object {
	rt := mi.vu.Runtime()

	if len(cc.Arguments) < 1 {
		common.Throw(rt, errors.New("Client constructor expects a single configuration object as argument; none given"))
	}

	var opts options
	if err := rt.ExportTo(cc.Arguments[0], &opts); err != nil {
		common.Throw(rt, fmt.Errorf("unable to parse options object; reason: %w", err))
	}

	return rt.ToValue(NewClient(mi.vu, opts)).ToObject(rt)
}

// InstrumentHTTP instruments the HTTP module with tracing headers.
//
// When used in the context of a k6 script, it will automatically replace
// the imported http module's methods with instrumented ones.
func (mi *ModuleInstance) instrumentHTTP(options options) {
	rt := mi.vu.Runtime()

	if mi.vu.State() != nil {
		common.Throw(rt, common.NewInitContextError("tracing module's instrumentHTTP can only be called in the init context"))
	}

	if mi.Client != nil {
		err := errors.New(
			"tracing module's instrumentHTTP can only be called once. " +
				"if you were attempting to reconfigure the instrumentation, " +
				"please consider using the tracing.Client instead",
		)
		common.Throw(rt, err)
	}

	// Initialize the tracing module's instance default client,
	// and configure it using the user-supplied set of options.
	mi.Client = NewClient(mi.vu, options)

	// Explicitly inject the http module in the VU's runtime.
	// This allows us to later on override the http module's methods
	// with instrumented ones.
	httpModuleValue, err := rt.RunString(`require('k6/http')`)
	if err != nil {
		common.Throw(rt, err)
	}

	httpModuleObj := httpModuleValue.ToObject(rt)

	// Closure overriding a method of the provided imported module object.
	//
	// The `onModule` argument should be a *goja.Object obtained by requiring
	// or importing the 'k6/http' module and converting it to an object.
	//
	// The `value` argument is expected to be callable.
	mustSetHTTPMethod := func(method string, onModule *goja.Object, value interface{}) {
		// Inject the new get function, adding tracing headers
		// to the request in the HTTP module object.
		err = onModule.Set(method, value)
		if err != nil {
			common.Throw(
				rt,
				fmt.Errorf("unable to overwrite http.%s method with instrumented one; reason: %w", method, err),
			)
		}
	}

	// Overwrite the implementation of the http module's method with the instrumented
	// ones exposed by the `tracing.Client` struct.
	mustSetHTTPMethod("del", httpModuleObj, mi.Client.Del)
	mustSetHTTPMethod("get", httpModuleObj, mi.Client.Get)
	mustSetHTTPMethod("head", httpModuleObj, mi.Client.Head)
	mustSetHTTPMethod("options", httpModuleObj, mi.Client.Options)
	mustSetHTTPMethod("patch", httpModuleObj, mi.Client.Patch)
	mustSetHTTPMethod("post", httpModuleObj, mi.Client.Patch)
	mustSetHTTPMethod("put", httpModuleObj, mi.Client.Patch)
	mustSetHTTPMethod("request", httpModuleObj, mi.Client.Request)
}
