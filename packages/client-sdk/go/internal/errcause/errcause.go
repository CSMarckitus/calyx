// Package errcause lets the other packages of the SDK set the private cause of a *calyx.Error.
//
// The cause stays unexported in calyx, so that a program cannot set it and no text of it reaches
// Error(). This package does not import calyx: calyx imports it and assigns Set in its init
// function. Go runs that init before any package that imports calyx, so Set is never nil there.
package errcause

// Set stores cause in the *calyx.Error that err is or wraps. It does nothing for another error.
var Set func(err, cause error)
