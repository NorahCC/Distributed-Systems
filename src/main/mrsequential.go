package main

//
// simple sequential MapReduce.
//
// go run mrsequential.go wc.so pg*.txt
//

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"plugin"
	"sort"

	"6.824/mr"
)

// for sorting by key.
type ByKey []mr.KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

/*
class ByKey implements SortInterface {

    List<KeyValue> arr;

    public int len() {
        return arr.size();
    }

    public void swap(int i, int j) {
        ...
    }

    public boolean less(int i, int j) {
        ...
    }
}

Go 的 sort 包定义了一个 interface：
type Interface interface {
    Len() int
    Less(i, j int) bool
    Swap(i, j int)
}

ByKey 通过实现 sort.Interface 来支持排序。
任何类型 只要有 Len Less Swap 方法，就可以被 sort 包的函数排序。
这三个func本质不是三个独立函数，而是 ByKey 类型的三个方法。
(a ByKey) 相当于java的this

*/

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: mrsequential xxx.so inputfiles...\n")
		os.Exit(1)
	}

	mapf, reducef := loadPlugin(os.Args[1])

	//
	// read each input file,
	// pass it to Map,
	// accumulate the intermediate Map output.
	//
	intermediate := []mr.KeyValue{}
	for _, filename := range os.Args[2:] {
		file, err := os.Open(filename)
		if err != nil {
			log.Fatalf("cannot open %v", filename)
		}
		content, err := ioutil.ReadAll(file)
		if err != nil {
			log.Fatalf("cannot read %v", filename)
		}
		file.Close()
		kva := mapf(filename, string(content))
		intermediate = append(intermediate, kva...)
	}

	//
	// a big difference from real MapReduce is that all the
	// intermediate data is in one place, intermediate[],
	// rather than being partitioned into NxM buckets.
	//

	sort.Sort(ByKey(intermediate))

	oname := "mr-out-0"
	ofile, _ := os.Create(oname)

	//
	// call Reduce on each distinct key in intermediate[],
	// and print the result to mr-out-0.
	//
	i := 0
	for i < len(intermediate) {
		j := i + 1
		for j < len(intermediate) && intermediate[j].Key == intermediate[i].Key {
			j++
		}
		values := []string{}
		for k := i; k < j; k++ {
			values = append(values, intermediate[k].Value)
		}
		output := reducef(intermediate[i].Key, values)

		// this is the correct format for each line of Reduce output.
		fmt.Fprintf(ofile, "%v %v\n", intermediate[i].Key, output)

		i = j
	}

	ofile.Close()
}

// load the application Map and Reduce functions
// from a plugin file, e.g. ../mrapps/wc.so
func loadPlugin(filename string) (func(string, string) []mr.KeyValue, func(string, []string) string) {
	p, err := plugin.Open(filename)
	if err != nil {
		log.Fatalf("cannot load plugin %v", filename)
	}
	xmapf, err := p.Lookup("Map")
	if err != nil {
		log.Fatalf("cannot find Map in %v", filename)
	}
	mapf := xmapf.(func(string, string) []mr.KeyValue)
	/*
			相当于 Function<String,String> mapf = (Function<String,String>) xmapf;
			go.(type) 是 Go 的类型断言语法，用于将接口类型的值转换为具体类型。
		    xmapf 是通过 plugin.Lookup("Map") 获取的一个接口类型的值，它实际上是一个函数。
		    通过 xmapf.(func(string, string) []mr.KeyValue) 这个语法，我们将 xmapf 转换为一个具体的函数类型 func(string, string) []mr.KeyValue。
		    如果 xmapf 的实际类型与 func(string, string) []mr.KeyValue 不匹配，那么这个断言会导致运行时错误。
	*/
	xreducef, err := p.Lookup("Reduce")
	if err != nil {
		log.Fatalf("cannot find Reduce in %v", filename)
	}
	reducef := xreducef.(func(string, []string) string)

	return mapf, reducef
}

/*
1. 读取文件
2. 调用 map lambda
3. 收集 kv
4. sort
5. group by
6. reduce
7. 输出
*/

/*
Go没 class hierarchy，没有extends，implements 之类的概念。
Go 通过接口（interface）来实现多态。一个类型只要实现了接口定义的所有方法，就被认为实现了该接口。
Go 的接口是一种抽象类型，它定义了一组方法，但不提供具体的实现。任何类型只要实现了这些方法，就可以被视为实现了该接口。
Go 的接口是隐式的，不需要显式声明一个类型实现了哪个接口。这种设计使得 Go 的类型系统非常灵活和简洁。

Go更函数式，函数也是一种类型，可以作为参数传递，也可以作为返回值。 x := someFunction -》 调用：x(...)

Go的slice很核心，ArrayList + array的混合

Go的error处理方式是通过返回值来处理错误，而不是通过异常机制。这种设计使得错误处理更加显式和可控。value + error

Go interface是隐式实现
*/
