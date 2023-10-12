package errors

import (
	"fmt"
	"io"
	"path"
	"runtime"
	"strconv"
	"strings"
)

// Frame represents a program counter inside a stack frame.
// For historical reasons if Frame is interpreted as a uintptr
// its value represents the program counter + 1.
type Frame uintptr

// pc returns the program counter for this frame;
// multiple frames may have the same PC value.
func (f Frame) pc() uintptr { return uintptr(f) - 1 }

// file returns the full path to the file that contains the
// function for this Frame's pc.
func (f Frame) file() string {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return "unknown"
	}
	file, _ := fn.FileLine(f.pc())
	return file
}

// line returns the line number of source code of the
// function for this Frame's pc.
func (f Frame) line() int {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return 0
	}
	_, line := fn.FileLine(f.pc())
	return line
}

// name returns the name of this function, if known.
func (f Frame) name() string {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return "unknown"
	}
	return fn.Name()
}

// Format formats the frame according to the fmt.Formatter interface.
//
//	%s    source file
//	%d    source line
//	%n    function name
//	%v    equivalent to %s:%d
//
// Format accepts flags that alter the printing of some verbs, as follows:
//
//	%+s   function name and path of source file relative to the compile time
//	      GOPATH separated by \n\t (<funcname>\n\t<path>)
//	%+v   equivalent to %+s:%d
func (f Frame) Format(s fmt.State, verb rune) {
	switch verb {
	case 's':
		switch {
		case s.Flag('+'):
			io.WriteString(s, f.name())
			io.WriteString(s, "\n\t")
			io.WriteString(s, f.file())
		default:
			io.WriteString(s, path.Base(f.file()))
		}
	case 'd':
		io.WriteString(s, strconv.Itoa(f.line()))
	case 'n':
		io.WriteString(s, funcname(f.name()))
	case 'v':
		f.Format(s, 's')
		io.WriteString(s, ":")
		f.Format(s, 'd')
	}
}

// MarshalText formats a stacktrace Frame as a text string. The output is the
// same as that of fmt.Sprintf("%+v", f), but without newlines or tabs.
func (f Frame) MarshalText() ([]byte, error) {
	name := f.name()
	if name == "unknown" {
		return []byte(name), nil
	}
	return []byte(fmt.Sprintf("%s %s:%d", name, f.file(), f.line())), nil
}

// StackTrace is stack of Frames from innermost (newest) to outermost (oldest).
type StackTrace []Frame

// Format formats the stack of Frames according to the fmt.Formatter interface.
//
//	%s	lists source files for each Frame in the stack
//	%v	lists the source file and line number for each Frame in the stack
//
// Format accepts flags that alter the printing of some verbs, as follows:
//
//	%+v   Prints filename, function, and line number for each Frame in the stack.
func (st StackTrace) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		switch {
		case s.Flag('+'):
			for _, f := range st {
				io.WriteString(s, "\n")
				f.Format(s, verb)
			}
		case s.Flag('#'):
			fmt.Fprintf(s, "%#v", []Frame(st))
		default:
			st.formatSlice(s, verb)
		}
	case 's':
		st.formatSlice(s, verb)
	}
}

// formatSlice will format this StackTrace into the given buffer as a slice of
// Frame, only valid when called with '%s' or '%v'.
func (st StackTrace) formatSlice(s fmt.State, verb rune) {
	io.WriteString(s, "[")
	for i, f := range st {
		if i > 0 {
			io.WriteString(s, " ")
		}
		f.Format(s, verb)
	}
	io.WriteString(s, "]")
}

// stack represents a stack of program counters.
type stack []uintptr

func (s *stack) Format(st fmt.State, verb rune) {
	switch verb {
	case 'v':
		switch {
		case st.Flag('+'):
			for _, pc := range *s {
				f := Frame(pc)
				fmt.Fprintf(st, "\n%+v", f)
			}
		}
	}
}

func (s *stack) StackTrace() StackTrace {
	f := make([]Frame, len(*s))
	for i := 0; i < len(f); i++ {
		f[i] = Frame((*s)[i])
	}
	return f
}

// callers returns a stack trace of program counters starting from the caller's frame,
// skipping the specified number of frames.
//
// The returned stack represents the program counters of the calling functions.
// The depth of the stack is determined by the number of frames available and the skip value.
//
// Note: The skip value is used to control the number of frames to skip before recording
// in the program counters (pc). A default skip value of 3 is used, to add or subtract from
// the skip value provide a positive or negative skip value not exceeding -3 to avoid as
// passing a negative skip value to runtime.Callers may not yield meaningful results
func callers(skip int) *stack {
	const depth = 32
	var pcs [depth]uintptr
	n := runtime.Callers(skip+3, pcs[:])
	var st stack = pcs[0:n]
	return &st
}

// funcname removes the path prefix component of a function's name reported by func.Name().
func funcname(name string) string {
	i := strings.LastIndex(name, "/")
	name = name[i+1:]
	i = strings.Index(name, ".")
	return name[i+1:]
}

// ancestorOfCause returns true if the caller looks to be an ancestor of the given stack
// trace. We check this by seeing whether our stack prefix-matches the cause stack, which
// should imply the error was generated directly from our goroutine.
func ancestorOfCause(ourStack *stack, causeStack StackTrace) bool {
	// Stack traces are ordered such that the deepest frame is first. We'll want to check
	// for prefix matching in reverse.
	//
	// As an example, imagine we have a prefix-matching stack for ourselves:
	// [
	//   "github.com/onsi/ginkgo/internal/leafnodes.(*runner).runSync",
	//   "github.com/incident-io/core/server/pkg/errors_test.TestSuite",
	//   "testing.tRunner",
	//   "runtime.goexit"
	// ]
	//
	// We'll want to compare this against an error cause that will have happened further
	// down the stack. An example stack trace from such an error might be:
	// [
	//   "github.com/incident-io/core/server/pkg/errors.New",
	//   "github.com/incident-io/core/server/pkg/errors_test.glob..func1.2.2.2.1",
	//   "github.com/onsi/ginkgo/internal/leafnodes.(*runner).runSync",
	//   "github.com/incident-io/core/server/pkg/errors_test.TestSuite",
	//   "testing.tRunner",
	//   "runtime.goexit"
	// ]
	//
	// They prefix match, but we'll have to handle the match carefully as we need to match
	// from back to forward.

	// We can't possibly prefix match if our stack is larger than the cause stack.
	if len(*ourStack) > len(causeStack) {
		return false
	}

	// We know the sizes are compatible, so compare program counters from back to front.
	for idx := 0; idx < len(*ourStack); idx++ {
		if (*ourStack)[len(*ourStack)-1] != (uintptr)(causeStack[len(causeStack)-1]) {
			return false
		}
	}

	// All comparisons checked out, these stacks match.
	return true
}
