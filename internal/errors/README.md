# Error Handling Guidelines

This document defines the three error handling mechanisms used in GoAgent and when to use each.

## Three Error Handling Mechanisms

### 1. `internal/errors.Wrap()` - High-Performance Error Wrapping
**Use for:** Wrapping errors with context in high-frequency paths

```go
import "goagentx/internal/errors"

// Wrap an error with context
return errors.Wrap(err, "database query failed")

// Wrap with additional context
return errors.Wrap(err, "user authentication: invalid credentials")
```

**When to use:**
- Wrapping errors from external libraries (database, HTTP clients, etc.)
- Adding context to errors before propagating them up the call stack
- High-frequency error paths where performance matters

**Why:** Custom implementation is more efficient than `fmt.Errorf` for simple string concatenation.

### 2. `fmt.Errorf()` with `%w` - Standard Error Wrapping
**Use for:** Wrapping errors with formatted messages

```go
return fmt.Errorf("failed to process user %s: %w", userID, err)
```

**When to use:**
- When you need to format the error message with variables
- When the format string is complex and requires printf-style formatting
- When you need standard Go error unwrapping behavior

**Why:** Standard Go pattern, well-understood by the community, supports `errors.Is()` and `errors.As()`.

### 3. `fmt.Errorf()` without `%w` - Simple Error Creation
**Use for:** Creating new errors without wrapping

```go
return fmt.Errorf("invalid configuration: port must be between 1-65535")
```

**When to use:**
- Creating new errors that don't wrap another error
- Validation errors
- Configuration errors
- Simple error messages

**Why:** Clear and straightforward for simple error messages.

## Decision Tree

```
Are you wrapping an existing error?
├─ Yes → Do you need formatted output?
│         ├─ Yes → Use fmt.Errorf() with %w
│         └─ No → Use errors.Wrap()
└─ No → Use fmt.Errorf() without %w (or errors.Newf())
```

## Examples

### Database Error (High-frequency, simple context)
```go
rows, err := db.Query(ctx, query, args...)
if err != nil {
    return errors.Wrap(err, "failed to execute query")
}
```

### Validation Error (New error, no wrapping)
```go
if port < 1 || port > 65535 {
    return fmt.Errorf("invalid port: %d (must be 1-65535)", port)
}
```

### HTTP Request Error (Formatted output, wrapping)
```go
resp, err := http.Get(url)
if err != nil {
    return fmt.Errorf("failed to fetch %s: %w", url, err)
}
```

### API Layer Error (High-frequency, simple context)
```go
user, err := repo.GetUser(ctx, userID)
if err != nil {
    return errors.Wrap(err, "failed to get user")
}
```

## Migration Notes

When migrating code to use the appropriate error handling mechanism:

1. **For simple wrapping without formatting:** Change `fmt.Errorf("context: %w", err)` to `errors.Wrap(err, "context")`
2. **For new errors without wrapping:** Keep `fmt.Errorf()` or use `errors.Newf()` for consistency
3. **For formatted wrapping:** Keep `fmt.Errorf()` with `%w`

## Performance Considerations

- `errors.Wrap()` is ~2x faster than `fmt.Errorf()` for simple string concatenation
- Use `errors.Wrap()` in hot paths (database queries, HTTP requests, etc.)
- Use `fmt.Errorf()` when the performance difference is negligible
