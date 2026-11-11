const mb = (bytes: number) => Math.round(bytes / 1_048_576)

// One line of process memory in MB, for the logs. rss is what the container is
// charged for. heapUsed and heapTotal are V8, external and arrayBuffers are
// buffers V8 tracks outside its heap. What's left of rss is native memory,
// mostly the embedding model's runtime.
export function memoryLine(): string {
  const m = process.memoryUsage()
  return `rss=${mb(m.rss)} heapUsed=${mb(m.heapUsed)} heapTotal=${mb(m.heapTotal)} external=${mb(m.external)} arrayBuffers=${mb(m.arrayBuffers)} MB`
}
