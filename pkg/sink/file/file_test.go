package file

import (
	"context"
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/user/hermod/pkg/formatter/json"
	"github.com/user/hermod/pkg/message"
)

func TestFileSink(t *testing.T) {
	tmpfile, err := ioutil.TempFile("", "hermod-test-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	formatter := json.NewJSONFormatter()
	sink, err := NewFileSink(tmpfile.Name(), formatter)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	msg := message.AcquireMessage()
	msg.SetID("test-id")
	msg.SetTable("users")
	msg.SetSchema("public")
	msg.SetAfter([]byte(`{"name":"john"}`))

	err = sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	content, err := ioutil.ReadFile(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), "test-id") {
		t.Errorf("expected content to contain test-id, got %s", string(content))
	}
	if !strings.Contains(string(content), "users") {
		t.Errorf("expected content to contain users, got %s", string(content))
	}
}
