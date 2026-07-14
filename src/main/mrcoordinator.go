package main

//
// start the coordinator process, which is implemented
// in ../mr/coordinator.go
//
// go run mrcoordinator.go pg*.txt
//
// Please do not change this file.
//

import "6.824/mr"
import "time"
import "os"
import "fmt"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: mrcoordinator inputfiles...\n")
		os.Exit(1)
	}

	m := mr.MakeCoordinator(os.Args[1:], 10)
	// 创建coordinator 包含10个reducer 即10个reduce tasks map阶段结束后中间结果会被partition成10份

	for m.Done() == false {
		time.Sleep(time.Second)
	}

	time.Sleep(time.Second)
}

/*
step1:读取输入文件列表
step2:创建 Coordinator 对象，传入输入文件列表和 nReduce
step3:循环检查 Coordinator 是否完成，如果没有完成就睡眠1秒钟，继续检查
step4:当 Coordinator 完成后，睡眠1秒钟，退出

Coordinator 进程启动器， 它本身几乎没有 MapReduce 逻辑，真正逻辑在 mr/coordinator.go
文件作用类似于Java SpringBoot 的 main()，只负责1. 启动 coordinator 进程，2. 调用真正 Coordinator()
*/