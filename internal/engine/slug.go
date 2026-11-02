package engine

// Slug is the rule Theo uses to turn a project or worktree name into a
// Compose project name fragment: lowercase letters, digits, '-' and '_',
// with anything else replaced by '-' ("wt" when nothing is left). The proxy
// builds its host names from it, so a host name and a Compose project name
// agree on the same worktree.
func Slug(s string) string { return slug(s) }
