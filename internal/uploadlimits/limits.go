package uploadlimits

// Upload limits are shared by the HTTP middleware, file manager and SQL importer.
// Requests allow some room for multipart headers and regular form fields.
const (
    FileBytes int64 = 1 << 30
    MultipartBodyBytes int64 = FileBytes + (16 << 20)
    ExpandedBytes int64 = 4 * FileBytes
)
