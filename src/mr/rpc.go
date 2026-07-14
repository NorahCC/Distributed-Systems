package mr //library package not executable

//
// RPC definitions. Remote Procedure Call 像调用本地函数一样 调用远程函数
//
// remember to capitalize all names.
//

import "os"
import "strconv"

//
// example to show how to declare the arguments
// and reply for an RPC.
//
/*
type ExampleArgs struct {
	X int
}

type ExampleReply struct {
	Y int
}
*/
type InitWorkerArgs struct {
}

type InitWorkerReply struct {
	NMap    int
	NReduce int
}

type GetTaskArgs struct {
}

type GetTaskReply struct {
	Scheduled bool
	Phase     string
	TaskId    int
	Filename  string
	Content   string
}

type CommitTaskArgs struct {
	Phase  string
	TaskId int
}

type CommitTaskReply struct {
	Done bool
}
// Add your RPC definitions here.


// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func coordinatorSock() string {
	s := "/var/tmp/824-mr-"
	s += strconv.Itoa(os.Getuid())
	return s
}
