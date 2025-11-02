# Use Alpine for minimal attack surface
FROM golang:1.25.1-alpine AS builder

WORKDIR /app/temp/golang

# Install only essential build dependencies
RUN apk add --no-cache git

# Initialize Go module and pre-build standard library
RUN go mod init roguelearn.codebattle && \
    go build -v -o /dev/null std

# ============================================
# Final stage - minimal runtime image
# ============================================
FROM alpine:3.22.2

# Install only runtime dependencies (no build tools)
RUN apk add --no-cache \
    python3 \
    nodejs \
    bash \
    ca-certificates

# Create a non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Create temp directories with appropriate permissions
RUN mkdir -p /app/temp/golang /app/temp/python /app/temp/js && \
    chown -R appuser:appgroup /app/temp && \
    chmod -R 770 /app/temp

# Copy pre-built Go cache from builder
COPY --from=builder /go /go
COPY --from=builder /usr/local/go /usr/local/go

# Copy go.mod from builder to the golang temp directory
COPY --from=builder /app/temp/golang/go.mod /app/temp/golang/go.mod

# Set Go environment for the runtime
ENV PATH="/usr/local/go/bin:/go/bin:${PATH}" \
    GOPATH="/go" \
    GOCACHE="/go/cache"

# Ensure Go directories are accessible
RUN chown -R appuser:appgroup /go && chmod -R 755 /go

# Create placeholder files
RUN echo "// Temporary Go file" > /app/temp/golang/code.go && \
    echo "# Temporary Python file" > /app/temp/python/code.py && \
    echo "// Temporary JavaScript file" > /app/temp/js/code.js && \
    chown -R appuser:appgroup /app/temp && \
    chmod 660 /app/temp/*/*.go /app/temp/*/*.py /app/temp/*/*.js

# Fix ownership of go.mod
RUN chown appuser:appgroup /app/temp/golang/go.mod && \
    chmod 660 /app/temp/golang/go.mod

# Remove write permissions from system directories
RUN chmod 555 /bin /usr/bin /usr/local/bin 2>/dev/null || true

# Switch to non-root user
USER appuser

WORKDIR /app

# Set default command to keep the container running
CMD ["tail", "-f", "/dev/null"]
