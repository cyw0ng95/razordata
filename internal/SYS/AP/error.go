package AP

// This file re-exports error types from LOG/EC for backward compatibility.
// All new code should import "github.com/cyw0ng95/razordata/internal/LOG/EC" directly.
//
// LOG/EC is the single source of truth for Error/Kind/Code/SQLSTATE/Wrap/Classification.
// See internal/LOG/EC/error.go for the full implementation.
