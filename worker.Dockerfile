# Use Alpine for minimal attack surface
FROM golang:1.25.1-alpine

# Set the working directory
WORKDIR /app

# Install required dependencies
RUN apk add --no-cache \
    python3 \
    nodejs \
    musl-dev \
    curl \
    bash \
    git \
    cmake \
    make \
    coreutils \
    util-linux

# Create a non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Create the temp directory with appropriate permissions
RUN mkdir -p /app/temp/golang && chown appuser:appgroup /app/temp/golang && chmod 770 /app/temp/golang
RUN mkdir -p /app/temp/python && chown appuser:appgroup /app/temp/python && chmod 770 /app/temp/python
RUN mkdir -p /app/temp/js && chown appuser:appgroup /app/temp/js && chmod 770 /app/temp/js

# Create all the needed files with placeholder content
RUN echo "// Temporary Go file" > /app/temp/golang/code.go && \
    echo "# Temporary Python file" > /app/temp/python/code.py && \
    echo "// Temporary JavaScript file" > /app/temp/js/code.js

# Create compiler output destination
RUN touch /app/temp/exe && chown appuser:appgroup /app/temp/exe && chmod 770 /app/temp/exe

# Set permissions for all code files to be writable and executable
RUN chown appuser:appgroup /app/temp/golang/code.go /app/temp/python/code.py /app/temp/js/code.js && \
    chmod 660 /app/temp/golang/code.go /app/temp/python/code.py /app/temp/js/code.js

# Set permissions for the /app directory structure
RUN chmod 755 /app

# Remove unnecessary write permissions from root filesystem
RUN chmod 555 /bin /usr/bin /usr/local/bin

# Switch to non-root user
USER appuser

RUN cd /app/temp/golang && go mod init roguelearn.codebattle

# Build Go standard library to optimize performance
RUN go build -v -o /dev/null std

# Set default command to keep the container running
CMD [ "tail", "-f", "/dev/null" ]
# CMD[ "sleep", "infinity" ]
