# Stage 1: Build the Go application
FROM golang:1.26.10 AS builder

# The Node runtime may use a different Debian release from the Go builder.
# A static binary avoids a dependency on the builder's libc version.
ENV CGO_ENABLED=0 GOTOOLCHAIN=local

# Set the working directory inside the container
WORKDIR /app

# Copy the Go modules manifests
COPY go.mod go.sum ./

# Download Go module dependencies
RUN go mod download

# Copy the source code into the container
COPY . .

# Build the Go application
RUN go build -o /midnight-server ./cmd \
    && go version -m /midnight-server

# Stage 2: Set up the Node.js environment
FROM node:22

# Set the working directory inside the container
WORKDIR /app

# Copy the compiled Go binary from the builder stage
COPY --from=builder /midnight-server /app/midnight-server

# Copy the Node.js project files
COPY package.json yarn.lock ./

# Install Node.js dependencies
RUN yarn install --frozen-lockfile

# Copy the rest of the application files
COPY static ./static

COPY compact-installer.sh ./compact-installer.sh
ENV XDG_BIN_HOME=/usr/local/bin/compact_bin
ENV PATH=/usr/local/bin/compact_bin:$PATH

RUN ["sh", "./compact-installer.sh"]

RUN printf '\n. /usr/local/bin/compact_bin/env\n' >> /home/node/.profile
RUN printf '\n. /usr/local/bin/compact_bin/env\n' >> /home/node/.bashrc
RUN mkdir -p /home/node/.compact /app/tmp /app/static/verified \
    && chown -R node:node /home/node/.compact /home/node/.profile /home/node/.bashrc /app/tmp /app/static/verified

# k8s runs as uid `node`. compact stores toolchains under $HOME/.compact; installing
# as root leaves them in /root/.compact and preview verify fails with
# "Couldn't find compiler for x86_64-unknown-linux-musl".
USER node
ENV HOME=/home/node
# Bake historical compilers plus Mainnet/Preview and Stagenet Ledger 9 compilers.
RUN compact update 0.28.0 && compact update 0.29.0 && compact update 0.30.0 && compact update 0.31.0 && compact update 0.35.0 && compact update 0.36.0 && compact update 0.31.1

# Expose the application port (if applicable)
EXPOSE 8080

# Command to run the application
CMD ["./midnight-server"]
