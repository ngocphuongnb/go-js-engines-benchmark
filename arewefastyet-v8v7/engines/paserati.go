package engines

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nooga/paserati/pkg/builtins"
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/lexer"
	"github.com/nooga/paserati/pkg/parser"
	"github.com/nooga/paserati/pkg/source"
	"github.com/nooga/paserati/pkg/types"
	"github.com/nooga/paserati/pkg/vm"
)

type Paserati struct {
	p       *driver.Paserati
	output  [][]string
	baseDir string
}

func (p *Paserati) Name() string {
	return "Paserati"
}

func (p *Paserati) Init() error {
	// Get absolute path for the v8-v7 directory
	absDir, err := filepath.Abs("v8-v7")
	if err != nil {
		return fmt.Errorf("error resolving v8-v7 directory: %v", err)
	}
	p.baseDir = absDir

	// Create custom initializer for V8 benchmark builtins
	v8Init := &v8BenchInitializer{
		engine:  p,
		baseDir: absDir,
	}

	// Get standard initializers and add our custom one
	standardInits := builtins.GetStandardInitializers()
	customInits := append(standardInits, v8Init)

	// Create Paserati session with custom initializers and base directory
	p.p = driver.NewPaseratiWithInitializersAndBaseDir(customInits, absDir)

	// Give the initializer a reference to paserati so load() can compile scripts
	v8Init.paserati = p.p

	// Disable type checking for JavaScript files
	p.p.SetIgnoreTypeErrors(true)

	return nil
}

func (p *Paserati) Close() error {
	if p.p != nil {
		p.p.Cleanup()
	}
	p.output = nil
	p.p = nil
	return nil
}

func (p *Paserati) Run(inputFile string) ([][]string, error) {
	// Read the run.js entry point
	entryPoint := filepath.Join(p.baseDir, "run.js")
	sourceBytes, err := os.ReadFile(entryPoint)
	if err != nil {
		return nil, fmt.Errorf("error reading entry point: %v", err)
	}

	sourceCode := string(sourceBytes)

	// Parse the source code
	sourceFile := source.FromFile("run.js", sourceCode)
	lx := lexer.NewLexerWithSource(sourceFile)
	parseInstance := parser.NewParser(lx)
	program, parseErrs := parseInstance.ParseProgram()
	if len(parseErrs) > 0 {
		return nil, fmt.Errorf("parse error: %v", parseErrs[0])
	}

	// Compile using the Paserati session's compiler
	chunk, compileErrs := p.p.CompileProgram(program)
	if len(compileErrs) > 0 {
		return nil, fmt.Errorf("compile error: %v", compileErrs[0])
	}

	if chunk == nil {
		return nil, fmt.Errorf("compilation returned nil chunk")
	}

	// Execute
	_, runtimeErrs := p.p.GetVM().Interpret(chunk)
	if len(runtimeErrs) > 0 {
		// Return partial results even on error
		return p.output, fmt.Errorf("runtime error: %v", runtimeErrs[0])
	}

	return p.output, nil
}

var _ JSEngine = (*Paserati)(nil)

// v8BenchInitializer provides print and load functions for the v8-v7 benchmarks
type v8BenchInitializer struct {
	engine   *Paserati
	baseDir  string
	paserati *driver.Paserati
}

func (v *v8BenchInitializer) Name() string {
	return "V8Bench"
}

func (v *v8BenchInitializer) Priority() int {
	return 1000 // Run after standard builtins
}

func (v *v8BenchInitializer) InitTypes(ctx *builtins.TypeContext) error {
	// Define load function type: (filename: string) => void
	loadFunctionType := types.NewSimpleFunction([]types.Type{types.String}, types.Void)
	if err := ctx.DefineGlobal("load", loadFunctionType); err != nil {
		return err
	}

	// Define print function type: (...args: any[]) => void
	printFunctionType := types.NewVariadicFunction([]types.Type{}, types.Void, types.Any)
	if err := ctx.DefineGlobal("print", printFunctionType); err != nil {
		return err
	}

	return nil
}

func (v *v8BenchInitializer) InitRuntime(ctx *builtins.RuntimeContext) error {
	vmInstance := ctx.VM

	// Create print function - outputs to stdout and captures output
	initV := v
	printFunc := vm.NewNativeFunction(0, true, "print", func(args []vm.Value) (vm.Value, error) {
		parts := make([]string, len(args))
		for i, arg := range args {
			parts[i] = arg.ToString()
		}
		line := strings.Join(parts, " ")
		fmt.Println(line)
		initV.engine.output = append(initV.engine.output, []string{line})
		return vm.Undefined, nil
	})

	if err := ctx.DefineGlobal("print", printFunc); err != nil {
		return err
	}

	// Create load function - loads and executes script in same context
	loadFunc := vm.NewNativeFunction(1, false, "load", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 1 {
			return vm.Undefined, fmt.Errorf("load: missing filename argument")
		}

		filename := args[0].ToString()

		// Resolve relative path from base directory
		fullPath := filepath.Join(initV.baseDir, filename)

		// Read the file
		sourceBytes, err := os.ReadFile(fullPath)
		if err != nil {
			return vm.Undefined, fmt.Errorf("load: failed to read '%s': %v", filename, err)
		}

		sourceCode := string(sourceBytes)

		// Parse the source code
		sourceFile := source.FromFile(filename, sourceCode)
		lx := lexer.NewLexerWithSource(sourceFile)
		parseInstance := parser.NewParser(lx)
		program, parseErrs := parseInstance.ParseProgram()
		if len(parseErrs) > 0 {
			return vm.Undefined, fmt.Errorf("load: parse error in '%s': %v", filename, parseErrs[0])
		}

		// Compile using the Paserati session's compiler
		// This ensures we use the same global index allocation
		chunk, compileErrs := initV.paserati.CompileProgram(program)
		if len(compileErrs) > 0 {
			return vm.Undefined, fmt.Errorf("load: compile error in '%s': %v", filename, compileErrs[0])
		}

		if chunk == nil {
			return vm.Undefined, fmt.Errorf("load: compilation returned nil chunk for '%s'", filename)
		}

		// Execute in the same VM context
		_, runtimeErrs := vmInstance.Interpret(chunk)
		if len(runtimeErrs) > 0 {
			return vm.Undefined, fmt.Errorf("load: runtime error in '%s': %v", filename, runtimeErrs[0])
		}

		return vm.Undefined, nil
	})

	if err := ctx.DefineGlobal("load", loadFunc); err != nil {
		return err
	}

	return nil
}
