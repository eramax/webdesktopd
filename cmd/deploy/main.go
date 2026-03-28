// cmd/deploy: builds webdesktopd for the remote host, uploads via SCP, and starts it.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	host := flag.String("host", "127.0.0.1", "remote host")
	port := flag.String("port", "32233", "remote SSH port")
	user := flag.String("user", "abb", "remote user")
	pass := flag.String("pass", "", "remote password")
	remotePort := flag.String("remote-port", "18080", "port for webdesktopd on remote")
	flag.Parse()

	if *pass == "" {
		log.Fatal("--pass is required")
	}

	cfg := &ssh.ClientConfig{
		User:            *user,
		Auth:            []ssh.AuthMethod{ssh.Password(*pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%s", *host, *port)
	log.Printf("Connecting to %s...", addr)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		log.Fatalf("SSH dial: %v", err)
	}
	defer client.Close()
	log.Println("Connected.")

	// Detect remote architecture.
	goarch := runRemote(client, "uname -m")
	goarch = strings.TrimSpace(goarch)
	archMap := map[string]string{
		"x86_64":  "amd64",
		"aarch64": "arm64",
		"armv7l":  "arm",
	}
	targetArch, ok := archMap[goarch]
	if !ok {
		log.Fatalf("unknown remote arch %q", goarch)
	}
	goos := strings.TrimSpace(runRemote(client, "uname -s"))
	goos = strings.ToLower(goos)
	log.Printf("Remote: %s/%s → GOOS=%s GOARCH=%s", goos, goarch, goos, targetArch)

	// Kill any existing webdesktopd process on the remote.
	runRemoteIgnoreErr(client, "pkill -f webdesktopd || true")
	time.Sleep(500 * time.Millisecond)

	// Reuse a persistent JWT secret on the remote so browser tokens survive redeploys.
	secretPath := "~/.config/webdesktopd/env"
	jwtSecret := readRemoteJWTSecret(client, secretPath)
	if jwtSecret == "" {
		jwtSecret, err = generateJWTSecret()
		if err != nil {
			log.Fatalf("generate JWT secret: %v", err)
		}
		if err := writeRemoteJWTSecret(client, secretPath, jwtSecret); err != nil {
			log.Fatalf("persist JWT secret: %v", err)
		}
		log.Printf("Created persistent JWT secret at %s", secretPath)
	} else {
		log.Printf("Loaded persistent JWT secret from %s", secretPath)
	}

	// Build binary for remote target.
	binPath := "/tmp/webdesktopd-deploy"
	log.Printf("Building for %s/%s...", goos, targetArch)
	buildCmd := exec.Command("go", "build", "-o", binPath, "webdesktopd")
	buildCmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+targetArch, "CGO_ENABLED=0")
	buildCmd.Dir = "/mnt/data1/projects/webdesktopd"
	if out, err := buildCmd.CombinedOutput(); err != nil {
		log.Fatalf("build failed: %v\n%s", err, out)
	}
	log.Printf("Build OK: %s", binPath)

	// Upload via SCP.
	remotePath := "/tmp/webdesktopd"
	log.Printf("Uploading to %s:%s...", addr, remotePath)
	if err := scpUpload(client, binPath, remotePath); err != nil {
		log.Fatalf("SCP upload: %v", err)
	}
	log.Println("Upload complete.")

	// Make executable.
	runRemote(client, "chmod +x "+remotePath)

	// Start server in background.
	startCmd := fmt.Sprintf(
		"nohup env JWT_SECRET=%s %s -addr :%s -ssh-addr 127.0.0.1:22 > /tmp/webdesktopd.log 2>&1 & echo $!",
		shellQuote(jwtSecret), remotePath, *remotePort,
	)
	pid := strings.TrimSpace(runRemote(client, startCmd))
	log.Printf("Server started (PID %s) on remote port %s", pid, *remotePort)

	// Wait a moment then tail the log.
	time.Sleep(time.Second)
	log.Println("=== Remote log ===")
	fmt.Println(runRemoteIgnoreErr(client, "cat /tmp/webdesktopd.log"))

	// Verify it's responding.
	health := runRemoteIgnoreErr(client, fmt.Sprintf(
		"curl -s http://127.0.0.1:%s/health 2>/dev/null || echo 'not ready'", *remotePort,
	))
	log.Printf("Health check: %s", strings.TrimSpace(health))

	fmt.Printf("\n✓ webdesktopd running on build-server:%s\n", *remotePort)
	fmt.Printf("  Access via SSH tunnel: ssh -p %s -L 18080:127.0.0.1:%s %s@%s\n",
		*port, *remotePort, *user, *host)
	fmt.Printf("  Then open: http://localhost:18080\n")
}

// runRemote executes a command on the remote host and returns stdout.
func runRemote(client *ssh.Client, cmd string) string {
	sess, err := client.NewSession()
	if err != nil {
		log.Fatalf("new session: %v", err)
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	if err != nil {
		log.Fatalf("remote command %q: %v\nOutput: %s", cmd, err, out)
	}
	return string(out)
}

func runRemoteIgnoreErr(client *ssh.Client, cmd string) string {
	sess, err := client.NewSession()
	if err != nil {
		return ""
	}
	defer sess.Close()
	out, _ := sess.CombinedOutput(cmd)
	return string(out)
}

func runRemoteOutput(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

func readRemoteJWTSecret(client *ssh.Client, envPath string) string {
	script := fmt.Sprintf("cat %s 2>/dev/null || true", shellQuote(envPath))
	content := strings.TrimSpace(runRemoteIgnoreErr(client, script))
	return parseJWTSecretEnv(content)
}

func writeRemoteJWTSecret(client *ssh.Client, envPath, jwtSecret string) error {
	dir := path.Dir(envPath)
	script := fmt.Sprintf(
		"umask 077; mkdir -p %s; printf 'JWT_SECRET=%%s\\n' %s > %s; chmod 600 %s",
		shellQuote(dir), shellQuote(jwtSecret), shellQuote(envPath), shellQuote(envPath),
	)
	if out, err := runRemoteOutput(client, script); err != nil {
		return fmt.Errorf("write remote secret: %w (output: %s)", err, strings.TrimSpace(out))
	}
	return nil
}

func generateJWTSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func parseJWTSecretEnv(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "JWT_SECRET=") {
			return strings.TrimPrefix(line, "JWT_SECRET=")
		}
	}
	return ""
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// scpUpload uploads a local file to the remote via the SCP sink protocol.
func scpUpload(client *ssh.Client, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat local file: %w", err)
	}

	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	stdin, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	var stderr bytes.Buffer
	sess.Stderr = &stderr

	if err := sess.Start("scp -t " + remotePath); err != nil {
		return fmt.Errorf("start scp: %w", err)
	}

	// Send file header.
	header := fmt.Sprintf("C0755 %d webdesktopd\n", info.Size())
	if _, err := io.WriteString(stdin, header); err != nil {
		return fmt.Errorf("write scp header: %w", err)
	}

	// Send file content.
	if _, err := io.Copy(stdin, f); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}

	// Send null terminator.
	if _, err := stdin.Write([]byte{0}); err != nil {
		return fmt.Errorf("write null: %w", err)
	}

	stdin.Close()
	if err := sess.Wait(); err != nil {
		return fmt.Errorf("scp wait: %v (stderr: %s)", err, stderr.String())
	}
	return nil
}
