package nats

import (
	"context"
	"crypto/subtle"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// TaskUserPrefix names a task's broker user: "task-<id>". Its password is the
// task's run credential.
const TaskUserPrefix = "task-"

// TaskInboxPrefix is the inbox prefix a task's connection must use: the only
// inbox it may subscribe to.
func TaskInboxPrefix(taskID int64) string {
	return "_INBOX." + TaskUserPrefix + strconv.FormatInt(taskID, 10)
}

// TaskSubjects is what one task's connection may publish to and subscribe to,
// beside its own inbox and its replies.
type TaskSubjects struct {
	Publish   []string
	Subscribe []string
}

// TaskAuthority answers which subjects a run credential opens for its task.
// An error refuses the connection.
type TaskAuthority interface {
	TaskSubjects(ctx context.Context, taskID int64, credential string) (TaskSubjects, error)
}

// taskAuthTimeout bounds the credential lookup a connection waits on.
const taskAuthTimeout = 2 * time.Second

// authenticator admits the instance token with every permission, and a task
// user only to its own task's subjects, so no container holds a credential
// that reaches past its run.
type authenticator struct {
	token string
	tasks TaskAuthority
}

func (a authenticator) Check(c server.ClientAuthentication) bool {
	opts := c.GetOpts()
	if opts.Token != "" {
		if subtle.ConstantTimeCompare([]byte(opts.Token), []byte(a.token)) != 1 {
			return false
		}
		c.RegisterUser(&server.User{Username: "archie"})
		return true
	}
	taskID, ok := taskUser(opts.Username)
	if !ok || opts.Password == "" || a.tasks == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), taskAuthTimeout)
	defer cancel()
	subjects, err := a.tasks.TaskSubjects(ctx, taskID, opts.Password)
	if err != nil {
		return false
	}
	c.RegisterUser(&server.User{Username: opts.Username, Permissions: &server.Permissions{
		Publish:   &server.SubjectPermission{Allow: subjects.Publish},
		Subscribe: &server.SubjectPermission{Allow: append(subjects.Subscribe, TaskInboxPrefix(taskID)+".>")},
		// Lets the task answer the one request it is sent, on the requester's
		// reply subject.
		Response: &server.ResponsePermission{MaxMsgs: 1},
	}})
	return true
}

func taskUser(name string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimPrefix(name, TaskUserPrefix), 10, 64)
	if !strings.HasPrefix(name, TaskUserPrefix) || err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
