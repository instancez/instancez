package telemetry

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skip(err)
	}
	n, err := unix.IoctlGetUint32(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skip(err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := m.Read(buf); err != nil {
				return
			}
		}
	}()
	return m, s
}

// answerOnPrompt types answer into the pty once the prompt is written.
type answerOnPrompt struct {
	master *os.File
	answer string
	once   sync.Once
	sb     strings.Builder
}

func (a *answerOnPrompt) Write(p []byte) (int, error) {
	a.sb.Write(p)
	if strings.Contains(a.sb.String(), "Send? ") {
		a.once.Do(func() { _, _ = a.master.WriteString(a.answer) })
	}
	return len(p), nil
}

func waitInput(t *testing.T, f *os.File) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if n, _ := unix.IoctlGetInt(int(f.Fd()), unix.TIOCINQ); n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("typed-ahead input never arrived")
}

func TestPromptIgnoresTypedAheadInput(t *testing.T) {
	c := Init(Options{Version: "1.0.0", Home: t.TempDir(), Getenv: func(string) string { return "" }})
	for _, tc := range []struct {
		answer string
		want   bool
	}{{"n\n", false}, {"y\n", true}} {
		master, slave := openPTY(t)
		if _, err := master.WriteString("y\n"); err != nil {
			t.Fatal(err)
		}
		waitInput(t, slave)
		out := &answerOnPrompt{master: master, answer: tc.answer}
		if got := c.PromptCrash(sampleReport(), nil, slave, out, true); got != tc.want {
			t.Errorf("answer %q: got %v", tc.answer, got)
		}
	}
}
