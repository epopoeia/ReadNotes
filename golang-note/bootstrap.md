# Bootstrap

## 如何确定程序入口

思路，找到二进制文件的entry point，在debugger中确定代码位置

go tool compile -N -l -S test.go > test.s

使用gdb：info files可以找到入口地址

readelf：readelf -h ./for，再配合lldb的image lookup --address找到代码位置

```
lldb ./exec_file
(lldb) target create "./exec_file"
(lldb) command source -s 1 '/home/ubuntu/./.lldbinit'
(lldb) image lookup --address 0x448fc0
      Address: exec_file[0x0000000000448fc0] (exec_file..text + 294848)
      Summary: exec_file`_rt0_amd64_linux at rt0_linux_amd64.s:8

```

    note:mac可执行文件不是elf，所以不能用readelf，只能用gdb，info files看入口地址,b 入口地址断点，info b看断点信息

```
1       breakpoint     keep y   0x000000000105cda0 <_rt0_amd64_darwin>
```

## 启动流程

```
A(rt0_darwin_amd64.s:8<br/>_rt0_amd64_darwin) -->|JMP| B(asm_amd64.s:15<br/>_rt0_amd64)
B --> |JMP|C(asm_amd64.s:87<br/>runtime-rt0_go)
C --> D(runtime1.go:60<br/>runtime-args)
D --> E(os_darwin.go:396<br/>runtime-osinit)
E --> F(proc.go:532<br/>runtime-schedinit)
F --> G(proc.go:3382<br/>runtime-newproc)
G --> H(proc.go:1041<br/>runtime-mstart)
H --> I(在新创建的 p 和 m 上运行 runtime-main)
```

## 步骤

### _rt0_amd64_darwin

```
TEXT _rt0_amd64_darwin(SB),NOSPLIT,$-8
	JMP	_rt0_amd64(SB)
```
单纯的跳转

### _rt0_amd64

```
// _rt0_amd64 is common startup code for most amd64 systems when using
// internal linking. This is the entry point for the program from the
// kernel for an ordinary -buildmode=exe program. The stack holds the
// number of arguments and the C-style argv.
TEXT _rt0_amd64(SB),NOSPLIT,$-8
	MOVQ	0(SP), DI	// argc
	LEAQ	8(SP), SI	// argv
	JMP	runtime·rt0_go(SB)
```
64位可执行程序的内核认定入口，存储argc与argv，将程序的参数数量以及参数传送到寄存器

### runtime·rt0_go

```
TEXT runtime·rt0_go(SB),NOSPLIT,$0
	// copy arguments forward on an even stack
	MOVQ	DI, AX		// argc
	MOVQ	SI, BX		// argv
	SUBQ	$(4*8+7), SP		// 2args 2auto
	ANDQ	$~15, SP
	MOVQ	AX, 16(SP)
	MOVQ	BX, 24(SP)

	// 操作系统以及硬件信息检查

	LEAQ	runtime·m0+m_tls(SB), DI
	CALL	runtime·settls(SB)

	// store through it, to make sure it works
	get_tls(BX)
	MOVQ	$0x123, g(BX)
	MOVQ	runtime·m0+m_tls(SB), AX
	CMPQ	AX, $0x123
	JEQ 2(PC)
	CALL	runtime·abort(SB)
ok:
	// set the per-goroutine and per-mach "registers"
	get_tls(BX)
	LEAQ	runtime·g0(SB), CX
	MOVQ	CX, g(BX)
	LEAQ	runtime·m0(SB), AX

    // m0 g0绑定
	// save m->g0 = g0
	MOVQ	CX, m_g0(AX)
	// save m0 to g0->m
	MOVQ	AX, g_m(CX)

	CLD				// convention is D is always left cleared
	CALL	runtime·check(SB)

	MOVL	16(SP), AX		// copy argc
	MOVL	AX, 0(SP)
	MOVQ	24(SP), AX		// copy argv
	MOVQ	AX, 8(SP)
	CALL	runtime·args(SB)  // 参数处理
	CALL	runtime·osinit(SB) // 初始化操作系统信息
	CALL	runtime·schedinit(SB) // 初始化调度器

	// create a new goroutine to start program
	MOVQ	$runtime·mainPC(SB), AX		// entry 要在main goroutine上运行的函数
	PUSHQ	AX
	PUSHQ	$0			// arg size
	CALL	runtime·newproc(SB)  // 创建goroutine
	POPQ	AX
	POPQ	AX

	// start this M
	CALL	runtime·mstart(SB)  // 启动M

	CALL	runtime·abort(SB)	// mstart should never return
	RET

	// Prevent dead-code elimination of debugCallV1, which is
	// intended to be called by debuggers.
	MOVQ	$runtime·debugCallV1(SB), AX
	RET
```

### runtime·args

```go
runtime1.go:60

func args(c int32, v **byte) {
	argc = c
	argv = v
	sysargs(c, v)
}

os_darwin.go:396

func sysargs(argc int32, argv **byte) {
	// skip over argv, envv and the first string will be the path
	n := argc + 1
	for argv_index(argv, n) != nil {
		n++
	}
	executablePath = gostringnocopy(argv_index(argv, n+1))

	// strip "executable_path=" prefix if available, it's added after OS X 10.11.
	const prefix = "executable_path="
	if len(executablePath) > len(prefix) && executablePath[:len(prefix)] == prefix {
		executablePath = executablePath[len(prefix):]
	}
}
```
参数处理

### runtime·osinit

```go
os_darwin.go:125

// BSD interface for threading.
func osinit() {
	// pthread_create delayed until end of goenvs so that we
	// can look at the environment first.

	ncpu = getncpu()
	physPageSize = getPageSize()
}
```
初始化系统参数，获取cpu，mac上会获取页大小.

### runtime·schedinit

```go
proc.go:532

// The bootstrap sequence is:
//
//	call osinit
//	call schedinit
//	make & queue new G
//	call runtime·mstart
//
// The new G calls runtime·main.
// 启动流程
func schedinit() {
	// raceinit must be the first call to race detector.
    // In particular, it must be done before mallocinit below calls racemapshadow.
    // 从tls中获取g实例
	_g_ := getg()
	if raceenabled {
        // 必须在malloc前初始化
		_g_.racectx, raceprocctx0 = raceinit()
	}

    // 设置全局线程数上限
	sched.maxmcount = 10000

    // 初始化一系列函数所在的pc计数器，用于traceback
    tracebackinit()
    // 验证连接器符号正确性
    moduledataverify()
    // 一些全局栈初始化，主要是下面的stack pool
    // Global pool of spans that have free stacks.
	// Stacks are assigned an order according to size.
	//     order = log_2(size/FixedStack)
	// There is a free list for each order.
	// TODO: one lock per order?
	//var stackpool [_NumStackOrders]mSpanList

	// Global pool of large stack spans.
	//var stackLarge struct {
	//	lock mutex
	//	free [_MHeapMap_Bits]mSpanList // free lists by log_2(s.npages)
	//}
    stackinit()
    // 内存分配器初始化
    // 初始化全局的 mheap 和相应的 bitmap
    // malloc.go:415
	mallocinit()
    fastrandinit() // must run before mcommoninit
    // m 内部的一些变量初始化
	mcommoninit(_g_.m)
    cpuinit()       // must run before alginit
    // 初始化AES HASH算法,hash相关的依赖
	alginit()       // maps must not be used before this call
	modulesinit()   // provides activeModules
	typelinksinit() // uses maps, activeModules
	itabsinit()     // uses activeModules

	msigsave(_g_.m)
	initSigmask = _g_.m.sigmask

    // goargs 和 goenvs 是把原来 kernel 传入的 argv 和 envp 处理成自己的 argv 和 env
    // 获取命令行参数
    // 例：main test1 test2
    // 执行完后获得runtime.argslice=[]string len:3,cap:3,["main","test1","test2"]
    goargs()
    // 获取环境变量
	goenvs()
    parsedebugvars()
    // gc初始化
    // 读入 GOGC 环境变量，设置 GC 回收的触发 percent
	// 比如 GOGC=100，那么就是内存两倍的情况下触发回收
	// 如果 GOGC=300，那么就是内存四倍的情况下触发回收
	// 可以通过设置 GOGC=off 来彻底关闭 GC
	gcinit()

    sched.lastpoll = uint64(nanotime())
    // 检查p个数与cpu数量相关，使用GOMAXPROCS修改
	procs := ncpu
	if n, ok := atoi32(gogetenv("GOMAXPROCS")); ok && n > 0 {
		procs = n
    }
    // 修改 P数量
	if procresize(procs) != nil {
		throw("unknown runnable goroutine during bootstrap")
	}
}

```

### runtime·newproc

```go
proc.go:3382

// 在启动的时候，是把 runtime.main 传入到 newproc 函数中的
// 调度器执行过程中，这个函数很重要
// 创建一个新的 g，该 g 运行传入的这个函数
// 并把这个 g 放到 g 的 waiting 列表里等待执行
// 编译器会把 go func 编译成这个函数的调用

// Create a new g running fn with siz bytes of arguments.
// Put it on the queue of g's waiting to run.
// The compiler turns a go statement into a call to this.
// Cannot split the stack because it assumes that the arguments
// are available sequentially after &fn; they would not be
// copied if a stack split occurred.
//go:nosplit
func newproc(siz int32, fn *funcval) {
	argp := add(unsafe.Pointer(&fn), sys.PtrSize)
	gp := getg()
	pc := getcallerpc()
	systemstack(func() {
		newproc1(fn, argp, siz, gp, pc)
	})
}

```

### runtime·mstart

```go
proc.go:1031
// 启动M
// mstart is the entry-point for new Ms.
//
// This must not split the stack because we may not even have stack
// bounds set up yet.
//
// May run during STW (because it doesn't have a P yet), so write
// barriers are not allowed.
//
//go:nosplit
//go:nowritebarrierrec
func mstart() {
	_g_ := getg()

	osStack := _g_.stack.lo == 0
	if osStack {
		// Initialize stack bounds from system stack.
		// Cgo may have left stack size in stack.hi.
		// minit may update the stack bounds.
		size := _g_.stack.hi
		if size == 0 {
			size = 8192 * sys.StackGuardMultiplier
		}
		_g_.stack.hi = uintptr(noescape(unsafe.Pointer(&size)))
		_g_.stack.lo = _g_.stack.hi - size + 1024
	}
	// Initialize stack guard so that we can start calling regular
	// Go code.
	_g_.stackguard0 = _g_.stack.lo + _StackGuard
	// This is the g0, so we can also call go:systemstack
	// functions, which check stackguard1.
	_g_.stackguard1 = _g_.stackguard0
	mstart1()

	// Exit this thread.
	switch GOOS {
	case "windows", "solaris", "illumos", "plan9", "darwin", "aix":
		// Windows, Solaris, illumos, Darwin, AIX and Plan 9 always system-allocate
		// the stack, but put it in _g_.stack before mstart,
		// so the logic above hasn't set osStack yet.
		osStack = true
	}
	mexit(osStack)
}

```

### runtime·main

```go
proc.go:113

// 主goroutine
// The main goroutine.
func main() {
    // 获取tls中的g
	g := getg()

	// Racectx of m0->g0 is used only as the parent of the main goroutine.
	// It must not be used for anything else.
	g.m.g0.racectx = 0

	// Max stack size is 1 GB on 64-bit, 250 MB on 32-bit.
	// Using decimal instead of binary GB and MB because
    // they look nicer in the stack overflow failure message.
    // 为了打印信息好看
	if sys.PtrSize == 8 {
		maxstacksize = 1000000000
	} else {
		maxstacksize = 250000000
	}

	// Allow newproc to start new Ms.
	mainStarted = true

    // sysmon监控线程，不属于gpm体系，不需要p可直接运行，相当于后台线程
    // sysmon中有对checkdead的调用，即main goroutine deadlock报错的发源地
	if GOARCH != "wasm" { // no threads on wasm yet, so no sysmon
		systemstack(func() {
			newm(sysmon, nil)
		})
	}

	// Lock the main goroutine onto this, the main OS thread,
	// during initialization. Most programs won't care, but a few
	// do require certain calls to be made by the main thread.
	// Those can arrange for main.main to run in the main thread
	// by calling runtime.LockOSThread during initialization
	// to preserve the lock.
	lockOSThread()

	if g.m != &m0 {
		throw("runtime.main not on m0")
	}

    // 执行runtime里面所有的init函数
    // 编译器动态生成，不是实际实现的函数
    // 使用反编译工具查看
    // go tool objdump -s "runtime.\.init\b" xxxx 来查看实际的内容
    // runtime_init() 
	doInit(&runtime_inittask) // must be before defer
	if nanotime() == 0 {
		throw("nanotime returning zero")
	}

	// Defer unlock so that runtime.Goexit during init does the unlock too.
	needUnlock := true
	defer func() {
		if needUnlock {
			unlockOSThread()
		}
	}()

	// Record when the world started.
	runtimeInitTime = nanotime()

    // 启动后台垃圾回收器
	gcenable()

    main_init_done = make(chan bool)
    
    // 与runtime_init差不多
    // 负责非runtime包的init操作
    // fn := main_init // make an indirect call, as the linker doesn't know the address of the main package when laying down the runtime
	// fn()
	doInit(&main_inittask)

	close(main_init_done)

	needUnlock = false
	unlockOSThread()

	if isarchive || islibrary {
		// A program compiled with -buildmode=c-archive or c-shared
		// has a main, but it is not executed.
		return
    }
    // 执行用户程序main.main
	fn := main_main // make an indirect call, as the linker doesn't know the address of the main package when laying down the runtime
	fn()
	if raceenabled {
		racefini()
	}

    // panic处理
	// Make racy client program work: if panicking on
	// another goroutine at the same time as main returns,
	// let the other goroutine finish printing the panic trace.
	// Once it does, it will exit. See issues 3934 and 20018.
	if atomic.Load(&runningPanicDefers) != 0 {
		// Running deferred functions should not take long.
		for c := 0; c < 1000; c++ {
			if atomic.Load(&runningPanicDefers) == 0 {
				break
			}
			Gosched()
		}
	}
	if atomic.Load(&panicking) != 0 {
		gopark(nil, nil, waitReasonPanicWait, traceEvGoStop, 1)
	}

	exit(0)
	for {
		var x *int32
		*x = 0
	}
}

```