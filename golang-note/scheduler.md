# 调度器

调度主要是如何从runtime到用户代码段的过程。

```
// The bootstrap sequence is:
//
//	call osinit
//	call schedinit
//	make & queue new G
//	call runtime·mstart
//
```
go进程启动过程

## 数据结构

goroutine

```go
// stack 描述的是 Go 的执行栈，下界和上界分别为 [lo, hi]
// 如果从传统内存布局的角度来讲，Go 的栈实际上是分配在 C 语言中的堆区的
// 所以才能比 ulimit -s 的 stack size 还要大(1GB)
type stack struct {
    lo uintptr
    hi uintptr
}

// g 的运行现场
type gobuf struct {
    sp   uintptr    // sp 寄存器
    pc   uintptr    // pc 寄存器
    g    guintptr   // g 指针
    ctxt unsafe.Pointer // 这个似乎是用来辅助 gc 的
    ret  sys.Uintreg
    lr   uintptr    // 这是在 arm 上用的寄存器，不用关心
    bp   uintptr    // 开启 GOEXPERIMENT=framepointer，才会有这个
}

type g struct {
    // g的执行栈，lo 和 hi 成员描述了栈的下界和上界内存地址
    stack       stack
    // 栈扩张时用到的
    // 在函数的栈增长 prologue 中用 sp 寄存器和 stackguard0 来做比较
    // 如果 sp 比 stackguard0 小(因为栈向低地址方向增长)，那么就触发栈拷贝和调度
    // 正常情况下 stackguard0 = stack.lo + StackGuard
    // 不过 stackguard0 在需要进行调度时，会被修改为 StackPreempt，以触发抢占
    stackguard0 uintptr
    // stackguard1 是在 C 栈增长 prologue 作对比的对象
    // 在 g0 和 gsignal 栈上，其值为 stack.lo+StackGuard
    // 在其它的栈上这个值是 ~0(按 0 取反)以触发 morestack 调用(并 crash)
    stackguard1 uintptr

    _panic         *_panic
    _defer         *_defer
    m              *m             // 当前与 g 绑定的 m
    sched          gobuf          // goroutine 的现场
    syscallsp      uintptr        // if status==Gsyscall, syscallsp = sched.sp to use during gc
    syscallpc      uintptr        // if status==Gsyscall, syscallpc = sched.pc to use during gc
    stktopsp       uintptr        // expected sp at top of stack, to check in traceback
    param          unsafe.Pointer // wakeup 时的传入参数
    atomicstatus   uint32
    stackLock      uint32 // sigprof/scang lock; TODO: fold in to atomicstatus
    goid           int64  // goroutine id
    waitsince      int64  // g 被阻塞之后的近似时间
    waitreason     string // if status==Gwaiting
    schedlink      guintptr
    preempt        bool     // 抢占标记，这个为 true 时，stackguard0 是等于 stackpreempt 的
    throwsplit     bool     // must not split stack
    raceignore     int8     // ignore race detection events
    sysblocktraced bool     // StartTrace has emitted EvGoInSyscall about this goroutine
    sysexitticks   int64    // syscall 返回之后的 cputicks，用来做 tracing
    traceseq       uint64   // trace event sequencer
    tracelastp     puintptr // last P emitted an event for this goroutine
    lockedm        muintptr // 如果调用了 LockOsThread，那么这个 g 会绑定到某个 m 上
    sig            uint32
    writebuf       []byte
    sigcode0       uintptr
    sigcode1       uintptr
    sigpc          uintptr
    gopc           uintptr // 创建该 goroutine 的语句的指令地址
    startpc        uintptr // goroutine 函数的指令地址
    racectx        uintptr
    waiting        *sudog         // sudog structures this g is waiting on (that have a valid elem ptr); in lock order
    cgoCtxt        []uintptr      // cgo traceback context
    labels         unsafe.Pointer // profiler labels
    timer          *timer         // time.Sleep 缓存的定时器
    selectDone     uint32         // 该 g 是否正在参与 select，是否已经有人从 select 中胜出
}

```

g进入等待状态，或者阻塞时，会包装为sudog结构，一个g可以被打包成多个sudog挂在不同的等待队列上

```go
// sudog 代表在等待列表里的 g，比如向 channel 发送/接收内容时
// 之所以需要 sudog 是因为 g 和同步对象之间的关系是多对多的
// 一个 g 可能会在多个等待队列中，所以一个 g 可能被打包为多个 sudog
// 多个 g 也可以等待在同一个同步对象上
// 因此对于一个同步对象就会有很多 sudog 了
// sudog 是从一个特殊的池中进行分配的。用 acquireSudog 和 releaseSudog 来分配和释放 sudog
type sudog struct {

    // 之后的这些字段都是被该 g 所挂在的 channel 中的 hchan.lock 来保护的
    // shrinkstack depends on
    // this for sudogs involved in channel ops.
    g *g

    // isSelect 表示一个 g 是否正在参与 select 操作
    // 所以 g.selectDone 必须用 CAS 来操作，以胜出唤醒的竞争
    isSelect bool
    next     *sudog
    prev     *sudog
    elem     unsafe.Pointer // data element (may point to stack)

    // 下面这些字段则永远都不会被并发访问
    // 对于 channel 来说，waitlink 只会被 g 访问
    // 对于信号量来说，所有的字段，包括上面的那些字段都只在持有 semaRoot 锁时才可以访问
    acquiretime int64
    releasetime int64
    ticket      uint32
    parent      *sudog // semaRoot binary tree
    waitlink    *sudog // g.waiting list or semaRoot
    waittail    *sudog // semaRoot
    c           *hchan // channel
}
```

m即为runtime中的线程，对应一个pthread，pthread也会对应唯一的内核线程(task_struct)

```go
type m struct {
    g0      *g     // 用来执行调度指令的 goroutine
    morebuf gobuf  // gobuf arg to morestack
    divmod  uint32 // div/mod denominator for arm - known to liblink

    // Fields not known to debuggers.
    procid        uint64       // for debuggers, but offset not hard-coded
    gsignal       *g           // signal-handling g
    goSigStack    gsignalStack // Go-allocated signal handling stack
    sigmask       sigset       // storage for saved signal mask
    tls           [6]uintptr   // thread-local storage (for x86 extern register)
    mstartfn      func()
    curg          *g       // 当前运行的用户 goroutine
    caughtsig     guintptr // goroutine running during fatal signal
    p             puintptr // attached p for executing go code (nil if not executing go code)
    nextp         puintptr
    id            int64
    mallocing     int32
    throwing      int32
    preemptoff    string // 该字段不等于空字符串的话，要保持 curg 始终在这个 m 上运行
    locks         int32
    softfloat     int32
    dying         int32
    profilehz     int32
    helpgc        int32
    spinning      bool // m 失业了，正在积极寻找工作~，进入自旋状态
    blocked       bool // m 正阻塞在 note 上
    inwb          bool // m 正在执行 write barrier
    newSigstack   bool // minit on C thread called sigaltstack
    printlock     int8
    incgo         bool   // m 正在执行 cgo call
    freeWait      uint32 // if == 0, safe to free g0 and delete m (atomic)
    fastrand      [2]uint32
    needextram    bool
    traceback     uint8
    ncgocall      uint64      // cgo 调用总计数
    ncgo          int32       // 当前正在执行的 cgo 订单计数
    cgoCallersUse uint32      // if non-zero, cgoCallers in use temporarily
    cgoCallers    *cgoCallers // cgo traceback if crashing in cgo call
    park          note
    alllink       *m // on allm
    schedlink     muintptr
    mcache        *mcache
    lockedg       guintptr
    createstack   [32]uintptr    // stack that created this thread.
    freglo        [16]uint32     // d[i] lsb and f[i]
    freghi        [16]uint32     // d[i] msb and f[i+16]
    fflag         uint32         // floating point compare flags
    lockedExt     uint32         // tracking for external LockOSThread
    lockedInt     uint32         // tracking for internal lockOSThread
    nextwaitm     muintptr       // 正在等待锁的下一个 m
    waitunlockf   unsafe.Pointer // todo go func(*g, unsafe.pointer) bool
    waitlock      unsafe.Pointer
    waittraceev   byte
    waittraceskip int
    startingtrace bool
    syscalltick   uint32
    thread        uintptr // thread handle
    freelink      *m      // on sched.freem

    // these are here because they are too large to be on the stack
    // of low-level NOSPLIT functions.
    libcall   libcall
    libcallpc uintptr // for cpu profiler
    libcallsp uintptr
    libcallg  guintptr
    syscall   libcall // 存储 windows 平台的 syscall 参数

    mOS
}
```
p逻辑处理器，代表执行任务时的上下文，通常m必须与p绑定才能执行
```go
type p struct {
    lock mutex

    id          int32
    status      uint32 // one of pidle/prunning/...
    link        puintptr
    schedtick   uint32     // 每次调用 schedule 时会加一
    syscalltick uint32     // 每次系统调用时加一
    sysmontick  sysmontick // 上次 sysmon 观察到的 tick 时间
    m           muintptr   // 和相关联的 m 的反向指针，如果 p 是 idle 的话，那这个指针是 nil
    mcache      *mcache
    racectx     uintptr

    deferpool    [5][]*_defer // pool of available defer structs of different sizes (see panic.go)
    deferpoolbuf [5][32]*_defer

    // Cache of goroutine ids, amortizes accesses to runtime·sched.goidgen.
    goidcache    uint64
    goidcacheend uint64

    // runnable 状态的 goroutine。访问时是不加锁的
    runqhead uint32
    runqtail uint32
    runq     [256]guintptr
    // runnext 非空时，代表的是一个 runnable 状态的 G，
    // 这个 G 是被 当前 G 修改为 ready 状态的，
    // 并且相比在 runq 中的 G 有更高的优先级
    // 如果当前 G 的还有剩余的可用时间，那么就应该运行这个 G
    // 运行之后，该 G 会继承当前 G 的剩余时间
    // If a set of goroutines is locked in a
    // communicate-and-wait pattern, this schedules that set as a
    // unit and eliminates the (potentially large) scheduling
    // latency that otherwise arises from adding the ready'd
    // goroutines to the end of the run queue.
    runnext guintptr

    // Available G's (status == Gdead)
    gfree    *g
    gfreecnt int32

    sudogcache []*sudog
    sudogbuf   [128]*sudog

    tracebuf traceBufPtr

    // traceSweep indicates the sweep events should be traced.
    // This is used to defer the sweep start event until a span
    // has actually been swept.
    traceSweep bool
    // traceSwept and traceReclaimed track the number of bytes
    // swept and reclaimed by sweeping in the current sweep loop.
    traceSwept, traceReclaimed uintptr

    palloc persistentAlloc // per-P to avoid mutex

    // Per-P GC state
    gcAssistTime         int64 // Nanoseconds in assistAlloc
    gcFractionalMarkTime int64 // Nanoseconds in fractional mark worker
    gcBgMarkWorker       guintptr
    gcMarkWorkerMode     gcMarkWorkerMode

    // 当前标记 worker 的开始时间，单位纳秒
    gcMarkWorkerStartTime int64

    // gcw is this P's GC work buffer cache. The work buffer is
    // filled by write barriers, drained by mutator assists, and
    // disposed on certain GC state transitions.
    gcw gcWork

    // wbBuf is this P's GC write barrier buffer.
    //
    // TODO: Consider caching this in the running G.
    wbBuf wbBuf

    runSafePointFn uint32 // if 1, run sched.safePointFn at next safe point

    pad [sys.CacheLineSize]byte
}
```
调度器，只有一个实例
```go
type schedt struct {
    // 下面两个变量需以原子访问访问。保持在 struct 顶部，以使其在 32 位系统上可以对齐
    goidgen  uint64
    lastpoll uint64

    lock mutex

    // 当修改 nmidle，nmidlelocked，nmsys，nmfreed 这些数值时
    // 需要记得调用 checkdead

    midle        muintptr // idle m's waiting for work
    nmidle       int32    // 当前等待工作的空闲 m 计数
    nmidlelocked int32    // 当前等待工作的被 lock 的 m 计数
    mnext        int64    // 当前预缴创建的 m 数，并且该值会作为下一个创建的 m 的 ID
    maxmcount    int32    // 允许创建的最大的 m 数量
    nmsys        int32    // number of system m's not counted for deadlock
    nmfreed      int64    // cumulative number of freed m's

    ngsys uint32 // number of system goroutines; updated atomically

    pidle      puintptr // 空闲 p's
    npidle     uint32
    nmspinning uint32 // See "Worker thread parking/unparking" comment in proc.go.

    // 全局的可运行 g 队列
    runqhead guintptr
    runqtail guintptr
    runqsize int32

    // dead G 的全局缓存
    gflock       mutex
    gfreeStack   *g
    gfreeNoStack *g
    ngfree       int32

    // sudog 结构的集中缓存
    sudoglock  mutex
    sudogcache *sudog

    // 不同大小的可用的 defer struct 的集中缓存池
    deferlock mutex
    deferpool [5]*_defer

    // 被设置了 m.exited 标记之后的 m，这些 m 正在 freem 这个链表上等待被 free
    // 链表用 m.freelink 字段进行链接
    freem *m

    gcwaiting  uint32 // gc is waiting to run
    stopwait   int32
    stopnote   note
    sysmonwait uint32
    sysmonnote note

    // safepointFn should be called on each P at the next GC
    // safepoint if p.runSafePointFn is set.
    safePointFn   func(*p)
    safePointWait int32
    safePointNote note

    profilehz int32 // cpu profiling rate

    procresizetime int64 // 上次修改 gomaxprocs 的纳秒时间
    totaltime      int64 // ∫gomaxprocs dt up to procresizetime
}
```

## gmp工作模式

m与p绑定之后可以执行当前p本地队列中的g，m也可以看到调度器全局队列中的g以及网络池中的g；本地mei没有g之后会去全局以及网络池中获取，实在没有会去其他p的本地队列中偷取g来运行。

## p初始化

程序初始化时会调用 procresize 初始化全局p数组串成链表放入 sched 的 pidle 队列中

```go
for i := nprocs - 1; i >= 0; i-- {
    p := allp[i]

    // ...
    // 设置 p 的状态
    p.status = _Pidle
    // 初始化时，所有 p 的 runq 都是空的，所以一定会走这个 if
    if runqempty(p) {
        // 将 p 放到全局调度器的 pidle 队列中
        pidleput(p)
    } else {
        // ...
    }
}
```
创建好的p直接放到调度器的链表中

## g初始化

g实际创建过程是由 `runtime.newproc`创建的，它会继续调用 `runtime.newproc1`

```go
func newproc(siz int32, fn *funcval) {
    // add 是一个指针运算，跳过函数指针
    // 把栈上的参数起始地址找到
    argp := add(unsafe.Pointer(&fn), sys.PtrSize)
    pc := getcallerpc()
    systemstack(func() {
        newproc1(fn, (*uint8)(argp), siz, pc)
    })
}

// funcval 是一个变长结构，第一个成员是函数指针
// 所以上面的 add 是跳过这个 fn
type funcval struct {
    fn uintptr
    // variable-size, fn-specific data here
}
```
其中getcallerpc返回的是调用函数之后的那条指令的地址，即callee函数返回时要执行的吓一跳指令的地址
systemstack是为了让m切换到g0上执行各种调度函数
```go
// For example:
//
// func f(arg1, arg2, arg3 int) {
//    pc := getcallerpc()
//    sp := getcallersp(unsafe.Pointer(&arg1))
//}
//
// These two lines find the PC and SP immediately following
// the call to f (where f will return).
//
```

newproc1工作流程

```
newproc1 --> newg
newg[gfget] --> nil{is nil?}
nil -->|yes|E[init stack]
nil -->|no|C[malg]
C --> D[set g status=> idle->dead]
D --> allgadd
E --> G[set g status=> dead-> runnable]
allgadd --> G
G --> runqput
```

```go
func newproc1(fn *funcval, argp *uint8, narg int32, callerpc uintptr) {
    _g_ := getg()

    if fn == nil {
        _g_.m.throwing = -1 // do not dump full stacks
        throw("go of nil func value")
    }
    _g_.m.locks++ // disable preemption because it can be holding p in a local var
    siz := narg
    siz = (siz + 7) &^ 7


    _p_ := _g_.m.p.ptr()
    newg := gfget(_p_)
    if newg == nil {
        newg = malg(_StackMin)
        casgstatus(newg, _Gidle, _Gdead)
        allgadd(newg) // publishes with a g->status of Gdead so GC scanner doesn't look at uninitialized stack.
    }

    totalSize := 4*sys.RegSize + uintptr(siz) + sys.MinFrameSize // extra space in case of reads slightly beyond frame
    totalSize += -totalSize & (sys.SpAlign - 1)                  // align to spAlign
    sp := newg.stack.hi - totalSize
    spArg := sp

    // 初始化 g，g 的 gobuf 现场，g 的 m 的 curg
    // 以及各种寄存器
    memclrNoHeapPointers(unsafe.Pointer(&newg.sched), unsafe.Sizeof(newg.sched))
    newg.sched.sp = sp
    newg.stktopsp = sp
    // 修改pc
    newg.sched.pc = funcPC(goexit) + sys.PCQuantum // +PCQuantum so that previous instruction is in same function
    newg.sched.g = guintptr(unsafe.Pointer(newg))
    gostartcallfn(&newg.sched, fn)
    newg.gopc = callerpc
    newg.startpc = fn.fn
    if _g_.m.curg != nil {
        newg.labels = _g_.m.curg.labels
    }

    casgstatus(newg, _Gdead, _Grunnable)

    newg.goid = int64(_p_.goidcache)
    _p_.goidcache++
    runqput(_p_, newg, true)

    if atomic.Load(&sched.npidle) != 0 && atomic.Load(&sched.nmspinning) == 0 && mainStarted {
        wakep()
    }
    _g_.m.locks--
    if _g_.m.locks == 0 && _g_.preempt { // restore the preemption request in case we've cleared it in newstack
        _g_.stackguard0 = stackPreempt
    }
}
```
`go func`调用 runqput 将g放入执行队列

### gostartcallfn

```go
// adjust Gobuf as if it executed a call to fn
// and then did an immediate gosave.
func gostartcallfn(gobuf *gobuf, fv *funcval) {
    var fn unsafe.Pointer
    if fv != nil {
        fn = unsafe.Pointer(fv.fn)
    } else {
        fn = unsafe.Pointer(funcPC(nilfunc))
    }
    gostartcall(gobuf, fn, unsafe.Pointer(fv))
}

// adjust Gobuf as if it executed a call to fn with context ctxt
// and then did an immediate gosave.
func gostartcall(buf *gobuf, fn, ctxt unsafe.Pointer) {
    sp := buf.sp
    if sys.RegSize > sys.PtrSize {
        sp -= sys.PtrSize
        *(*uintptr)(unsafe.Pointer(sp)) = 0
    }
    sp -= sys.PtrSize
    *(*uintptr)(unsafe.Pointer(sp)) = buf.pc // 注意这里，这个，这里的 buf.pc 实际上是 goexit 的 pc
    buf.sp = sp
    buf.pc = uintptr(fn)
    buf.ctxt = ctxt
}
```
gostartcall中把newproc1中设置到buf.pc中的goexit地址放到了到了goroutine栈顶，重置buf.pc为goroutine函数的位置。这样每个函数执行完之后都会通过RET指令将栈顶的goexit指令pop到pc中，然后每个goroutine执行完函数之后都会执行runtime.goexit进行清理工作并再次进入调度。

### runqput

goroutine创建之后要放入队列，不是立即执行，需要等待调度。
```go
// runqput 尝试把 g 放到本地执行队列中
// next 参数如果是 false 的话，runqput 会将 g 放到运行队列的尾部
// If next if false, runqput adds g to the tail of the runnable queue.
// If next is true, runqput puts g in the _p_.runnext slot.
// If the run queue is full, runnext puts g on the global queue.
// Executed only by the owner P.
func runqput(_p_ *p, gp *g, next bool) {
    if randomizeScheduler && next && fastrand()%2 == 0 {
        next = false
    }

    if next {
    retryNext:
        oldnext := _p_.runnext
        if !_p_.runnext.cas(oldnext, guintptr(unsafe.Pointer(gp))) {
            goto retryNext
        }
        if oldnext == 0 {
            return
        }
        // 把之前的 runnext 踢到正常的 runq 中
        gp = oldnext.ptr()
    }

retry:
    h := atomic.Load(&_p_.runqhead) // load-acquire, synchronize with consumers
    t := _p_.runqtail
    if t-h < uint32(len(_p_.runq)) {
        _p_.runq[t%uint32(len(_p_.runq))].set(gp)
        atomic.Store(&_p_.runqtail, t+1) // store-release, makes the item available for consumption
        return
    }
    if runqputslow(_p_, gp, h, t) {
        return
    }
    // 队列没有满的话，上面的 put 操作会成功
    goto retry
}
```

### runqputslow

```go
// 因为 slow，所以会一次性把本地队列里的多个 g (包含当前的这个) 放到全局队列
// 只会被 g 的 owner P 执行
func runqputslow(_p_ *p, gp *g, h, t uint32) bool {
    var batch [len(_p_.runq)/2 + 1]*g

    // 先从本地队列抓一批 g
    n := t - h
    n = n / 2
    if n != uint32(len(_p_.runq)/2) {
        throw("runqputslow: queue is not full")
    }
    for i := uint32(0); i < n; i++ {
        batch[i] = _p_.runq[(h+i)%uint32(len(_p_.runq))].ptr()
    }
    if !atomic.Cas(&_p_.runqhead, h, h+n) { // cas-release, commits consume
        return false
    }
    batch[n] = gp

    if randomizeScheduler {
        for i := uint32(1); i <= n; i++ {
            j := fastrandn(i + 1)
            batch[i], batch[j] = batch[j], batch[i]
        }
    }

    // 把这些 goroutine 构造成链表
    for i := uint32(0); i < n; i++ {
        batch[i].schedlink.set(batch[i+1])
    }

    // 将链表放到全局队列中
    lock(&sched.lock)
    globrunqputbatch(batch[0], batch[n], int32(n+1))
    unlock(&sched.lock)
    return true
}
```
全局调度器sched需要加锁，获取全局 sched.lock 锁，开销较大。p 和 g 在 m 中交互时，永远是单线程。

## m工作机制

runtime中有三种线程，祝线程，sysmon线程，普通用户线程。主线程在runtime中由全局变量`runtime.m0`表示。用户线程就是普通线程，与p绑定，执行g中的任务，前两种的线程只有一个实例，用户想成由多个实例。

### 主线程m0

主线程中跑`runtime.main`，为线性执行，没有跳转。

```
runtime.main --> A[init max stack size]
A --> B[systemstack execute -> newm -> sysmon]
B --> runtime.lockOsThread
runtime.lockOsThread --> runtime.init
runtime.init --> runtime.gcenable
runtime.gcenable --> main.init
main.init --> main.main
```
### sysmon线程

sysmon 在 `runtime.main`中启动，并不在m0上执行，会创建一个特殊的m专门执行，不需要p，脱离调度系统。
```
systemstack(func() {
    newm(sysmon, nil)
})
```
sysmon内部为死循环

- checkdead，检查是否所有的goroutine都已经死锁，如果是，直接 runtime.throw 强制退出，只在启动时做一次
- 将netpoll返回的结果注入到全局sched任务队列中
- 收回因为syscall而长时间阻塞的p，同时抢占执行时间过长的g
- 如果span内存闲置时间超过5min，释放掉

主要流程：

```
sysmon --> usleep
usleep --> checkdead
checkdead --> |every 10ms|C[netpollinited && lastpoll != 0]
C --> |yes|netpoll
netpoll --> injectglist
injectglist --> retake
C --> |no|retake
retake --> A[check forcegc needed]
A --> B[scavenge heap once in a while]
B --> usleep
```

```go
// sysmon 不需要绑定 P 就可以运行，所以不允许 write barriers
//
//go:nowritebarrierrec
func sysmon() {
    lock(&sched.lock)
    sched.nmsys++
    checkdead()
    unlock(&sched.lock)

    // 如果一个 heap span 在一次GC 之后 5min 都没有被使用过
    // 那么把它交还给操作系统
    scavengelimit := int64(5 * 60 * 1e9)

    if debug.scavenge > 0 {
        // Scavenge-a-lot for testing.
        forcegcperiod = 10 * 1e6
        scavengelimit = 20 * 1e6
    }

    lastscavenge := nanotime()
    nscavenge := 0

    lasttrace := int64(0)
    idle := 0 // how many cycles in succession we had not wokeup somebody
    delay := uint32(0)
    for {
        if idle == 0 { // 初始化时 20us sleep
            delay = 20
        } else if idle > 50 { // start doubling the sleep after 1ms...
            delay *= 2
        }
        if delay > 10*1000 { // 最多到 10ms
            delay = 10 * 1000
        }
        usleep(delay)
        if debug.schedtrace <= 0 && (sched.gcwaiting != 0 || atomic.Load(&sched.npidle) == uint32(gomaxprocs)) {
            lock(&sched.lock)
            if atomic.Load(&sched.gcwaiting) != 0 || atomic.Load(&sched.npidle) == uint32(gomaxprocs) {
                atomic.Store(&sched.sysmonwait, 1)
                unlock(&sched.lock)
                // Make wake-up period small enough
                // for the sampling to be correct.
                maxsleep := forcegcperiod / 2
                if scavengelimit < forcegcperiod {
                    maxsleep = scavengelimit / 2
                }
                shouldRelax := true
                if osRelaxMinNS > 0 {
                    next := timeSleepUntil()
                    now := nanotime()
                    if next-now < osRelaxMinNS {
                        shouldRelax = false
                    }
                }
                if shouldRelax {
                    osRelax(true)
                }
                notetsleep(&sched.sysmonnote, maxsleep)
                if shouldRelax {
                    osRelax(false)
                }
                lock(&sched.lock)
                atomic.Store(&sched.sysmonwait, 0)
                noteclear(&sched.sysmonnote)
                idle = 0
                delay = 20
            }
            unlock(&sched.lock)
        }
        // trigger libc interceptors if needed
        if *cgo_yield != nil {
            asmcgocall(*cgo_yield, nil)
        }
        // 如果 10ms 没有 poll 过 network，那么就 netpoll 一次
        lastpoll := int64(atomic.Load64(&sched.lastpoll))
        now := nanotime()
        if netpollinited() && lastpoll != 0 && lastpoll+10*1000*1000 < now {
            atomic.Cas64(&sched.lastpoll, uint64(lastpoll), uint64(now))
            gp := netpoll(false) // 非阻塞 -- 返回一个 goroutine 的列表
            if gp != nil {
                // Need to decrement number of idle locked M's
                // (pretending that one more is running) before injectglist.
                // Otherwise it can lead to the following situation:
                // injectglist grabs all P's but before it starts M's to run the P's,
                // another M returns from syscall, finishes running its G,
                // observes that there is no work to do and no other running M's
                // and reports deadlock.
                incidlelocked(-1)
                injectglist(gp)
                incidlelocked(1)
            }
        }
        // 接收在 syscall 状态阻塞的 P
        // 抢占长时间运行的 G
        if retake(now) != 0 {
            idle = 0
        } else {
            idle++
        }
        // 检查是否需要 force GC(两分钟一次的)
        if t := (gcTrigger{kind: gcTriggerTime, now: now}); t.test() && atomic.Load(&forcegc.idle) != 0 {
            lock(&forcegc.lock)
            forcegc.idle = 0
            forcegc.g.schedlink = 0
            injectglist(forcegc.g)
            unlock(&forcegc.lock)
        }
        // 每过一段时间扫描一次堆
        if lastscavenge+scavengelimit/2 < now {
            mheap_.scavenge(int32(nscavenge), uint64(now), uint64(scavengelimit))
            lastscavenge = now
            nscavenge++
        }
        if debug.schedtrace > 0 && lasttrace+int64(debug.schedtrace)*1000000 <= now {
            lasttrace = now
            schedtrace(debug.scheddetail > 0)
        }
    }
}
```

#### checkdead

```go
// 检查死锁的场景
// 该检查基于当前正在运行的 M 的数量，如果 0，那么就是 deadlock 了
// 检查的时候必须持有 sched.lock 锁
func checkdead() {
    // 对于 -buildmode=c-shared 或者 -buildmode=c-archive 来说
    // 没有 goroutine 正在运行也是 OK 的。因为调用这个库的程序应该是在运行的
    if islibrary || isarchive {
        return
    }

    // If we are dying because of a signal caught on an already idle thread,
    // freezetheworld will cause all running threads to block.
    // And runtime will essentially enter into deadlock state,
    // except that there is a thread that will call exit soon.
    if panicking > 0 {
        return
    }

    run := mcount() - sched.nmidle - sched.nmidlelocked - sched.nmsys
    if run > 0 {
        return
    }
    if run < 0 {
        print("runtime: checkdead: nmidle=", sched.nmidle, " nmidlelocked=", sched.nmidlelocked, " mcount=", mcount(), " nmsys=", sched.nmsys, "\n")
        throw("checkdead: inconsistent counts")
    }

    grunning := 0
    lock(&allglock)
    for i := 0; i < len(allgs); i++ {
        gp := allgs[i]
        if isSystemGoroutine(gp) {
            continue
        }
        s := readgstatus(gp)
        switch s &^ _Gscan {
        case _Gwaiting:
            grunning++
        case _Grunnable,
            _Grunning,
            _Gsyscall:
            unlock(&allglock)
            print("runtime: checkdead: find g ", gp.goid, " in status ", s, "\n")
            throw("checkdead: runnable g")
        }
    }
    unlock(&allglock)
    if grunning == 0 { // possible if main goroutine calls runtime·Goexit()
        throw("no goroutines (main called runtime.Goexit) - deadlock!")
    }

    // Maybe jump time forward for playground.
    gp := timejump()
    if gp != nil {
        casgstatus(gp, _Gwaiting, _Grunnable)
        globrunqput(gp)
        _p_ := pidleget()
        if _p_ == nil {
            throw("checkdead: no p for timer")
        }
        mp := mget()
        if mp == nil {
            // There should always be a free M since
            // nothing is running.
            throw("checkdead: no m for timer")
        }
        mp.nextp.set(_p_)
        notewakeup(&mp.park)
        return
    }

    getg().m.throwing = -1 // do not dump full stacks
    throw("all goroutines are asleep - deadlock!")
}
```

#### retake
```go
// forcePreemptNS is the time slice given to a G before it is
// preempted.
const forcePreemptNS = 10 * 1000 * 1000 // 10ms

func retake(now int64) uint32 {
    n := 0
    // Prevent allp slice changes. This lock will be completely
    // uncontended unless we're already stopping the world.
    lock(&allpLock)
    // We can't use a range loop over allp because we may
    // temporarily drop the allpLock. Hence, we need to re-fetch
    // allp each time around the loop.
    for i := 0; i < len(allp); i++ {
        _p_ := allp[i]
        if _p_ == nil {
            // 在 procresize 修改了 allp 但还没有创建新的 p 的时候
            // 会有这种情况
            continue
        }
        pd := &_p_.sysmontick
        s := _p_.status
        if s == _Psyscall {
            // 从 syscall 接管 P，如果它进行 syscall 已经经过了一个 sysmon 的 tick(至少 20us)
            t := int64(_p_.syscalltick)
            if int64(pd.syscalltick) != t {
                pd.syscalltick = uint32(t)
                pd.syscallwhen = now
                continue
            }
            // 一方面如果没有其它工作可做的话，我们不想接管 p
            // 但另一方面为了避免 sysmon 线程陷入沉睡，我们最终还是会接管这些 p
            if runqempty(_p_) && atomic.Load(&sched.nmspinning)+atomic.Load(&sched.npidle) > 0 && pd.syscallwhen+10*1000*1000 > now {
                continue
            }
            // 解开 allplock 的锁，然后就可以持有 sched.lock 锁了
            unlock(&allpLock)
            // Need to decrement number of idle locked M's
            // (pretending that one more is running) before the CAS.
            // Otherwise the M from which we retake can exit the syscall,
            // increment nmidle and report deadlock.
            incidlelocked(-1)
            if atomic.Cas(&_p_.status, s, _Pidle) {
                if trace.enabled {
                    traceGoSysBlock(_p_)
                    traceProcStop(_p_)
                }
                n++
                _p_.syscalltick++
                handoffp(_p_)
            }
            incidlelocked(1)
            lock(&allpLock)
        } else if s == _Prunning {
            // 如果 G 运行时间太长，那么抢占它
            t := int64(_p_.schedtick)
            if int64(pd.schedtick) != t {
                pd.schedtick = uint32(t)
                pd.schedwhen = now
                continue
            }
            if pd.schedwhen+forcePreemptNS > now {
                continue
            }
            preemptone(_p_)
        }
    }
    unlock(&allpLock)
    return uint32(n)
}
```
### 普通线程

普通线程即为GPM中的M，对应操作系统线程。

#### 线程创建
创建线程的函数即为 newm

```
newm --> newm1
newm1 --> newosproc
newosproc --> clone
```
最终使用 linux 的系统调用 clone 创建线程

```go
// 创建一个新的 m。该 m 会在启动时调用函数 fn，或者 schedule 函数
// fn 需要是 static 类型，且不能是在堆上分配的闭包。
// 运行 m 时，m.p 是有可能为 nil 的，所以不允许 write barriers
//go:nowritebarrierrec
func newm(fn func(), _p_ *p) {
    mp := allocm(_p_, fn)
    mp.nextp.set(_p_)
    mp.sigmask = initSigmask
    newm1(mp)
}
```
将传入的p传给m.nextp，m执行schedule时会将nextp取出进行绑定(m.p=m.nextp,m.nextp=nil)
```go
func newm1(mp *m) {
    execLock.rlock() // Prevent process clone.
    newosproc(mp, unsafe.Pointer(mp.g0.stack.hi))
    execLock.runlock()
}
```

```go
func newosproc(mp *m, stk unsafe.Pointer) {
    // Disable signals during clone, so that the new thread starts
    // with signals disabled. It will enable them in minit.
    var oset sigset
    sigprocmask(_SIG_SETMASK, &sigset_all, &oset)
    ret := clone(cloneFlags, stk, unsafe.Pointer(mp), unsafe.Pointer(mp.g0), unsafe.Pointer(funcPC(mstart)))
    sigprocmask(_SIG_SETMASK, &oset, nil)

    if ret < 0 {
        print("runtime: failed to create new OS thread (have ", mcount(), " already; errno=", -ret, ")\n")
        if ret == -_EAGAIN {
            println("runtime: may need to increase max user processes (ulimit -u)")
        }
        throw("newosproc")
    }
}
```

#### 工作流程

空闲的m会被放入全局调度器 midle 队列中，在需要m的时候从里面取
```go
//go:nowritebarrierrec
// 尝试从 midle 列表中获取一个 m
// 必须锁全局的 sched
// 可能在 STW 期间执行，所以不允许 write barriers
func mget() *m {
    mp := sched.midle.ptr()
    if mp != nil {
        sched.midle = mp.schedlink
        sched.nmidle--
    }
    return mp
}
```
娶不到的话会调用newm创建新的线程，创建的线程是不会销毁的，只会重新放入 midle中。

何时创建线程，即 newm 的调用方：
```
main --> |sysmon|newm
startTheWorld --> startTheWorldWithSema
gcMarkTermination --> startTheWorldWithSema
gcStart--> startTheWorldWithSema
startTheWorldWithSema --> |helpgc|newm
startTheWorldWithSema --> |run p|newm
startm --> mget
mget --> |if no free m|newm
startTemplateThread --> |templateThread|newm
LockOsThread --> startTemplateThread
main --> |iscgo|startTemplateThread
handoffp --> startm
wakep --> startm
injectglist --> startm
```
基本上为按需创建，如果没有空闲的m，需要的时候就创建一个。

创建好的线程需要绑定p才会开始执行，执行过程中可能会被剥夺 p。retake会将 g 的 stackguard0 修改为stackPreempt，进入下一次 newstack 时，回盘孤单抢占标记，如果有就放弃运行，即 `协作式调度`。

工作线程执行的核心即为：`schedule()`以及`findrunnable()`。

##### schedule
```
schedule --> A[schedtick%61 == 0]
A --> |yes|globrunqget
A --> |no|runqget
globrunqget --> C[gp == nil]
C --> |no|execute
C --> |yes|runqget
runqget --> B[gp == nil]
B --> |no|execute
B --> |yes|findrunnable
findrunnable --> execute
```

```go
// 调度器调度一轮要执行的函数: 寻找一个 runnable 状态的 goroutine，并 execute 它
// 调度函数是循环，永远都不会返回
func schedule() {
    _g_ := getg()

    if _g_.m.locks != 0 {
        throw("schedule: holding locks")
    }

    if _g_.m.lockedg != 0 {
        stoplockedm()
        execute(_g_.m.lockedg.ptr(), false) // Never returns.
    }

    // 执行 cgo 调用的 g 不能被 schedule 走
    // 因为 cgo 调用使用 m 的 g0 栈
    if _g_.m.incgo {
        throw("schedule: in cgo")
    }

top:
    if sched.gcwaiting != 0 {
        gcstopm()
        goto top
    }
    if _g_.m.p.ptr().runSafePointFn != 0 {
        runSafePointFn()
    }

    var gp *g
    var inheritTime bool
    if trace.enabled || trace.shutdown {
        gp = traceReader()
        if gp != nil {
            casgstatus(gp, _Gwaiting, _Grunnable)
            traceGoUnpark(gp, 0)
        }
    }
    if gp == nil && gcBlackenEnabled != 0 {
        gp = gcController.findRunnableGCWorker(_g_.m.p.ptr())
    }
    if gp == nil {
        // 每调度几次就检查一下全局的 runq 来确保公平
        // 否则两个 goroutine 就可以通过互相调用
        // 完全占用本地的 runq 了
        if _g_.m.p.ptr().schedtick%61 == 0 && sched.runqsize > 0 {
            lock(&sched.lock)
            gp = globrunqget(_g_.m.p.ptr(), 1)
            unlock(&sched.lock)
        }
    }
    if gp == nil {
        gp, inheritTime = runqget(_g_.m.p.ptr())
        if gp != nil && _g_.m.spinning {
            throw("schedule: spinning with local work")
        }
    }
    if gp == nil {
        gp, inheritTime = findrunnable() // 在找到 goroutine 之前会一直阻塞下去
    }

    // 当前线程将要执行 goroutine，并且不会再进入 spinning 状态
    // 所以如果它被标记为 spinning，我们需要 reset 这个状态
    // 可能会重启一个新的 spinning 状态的 M
    if _g_.m.spinning {
        resetspinning()
    }

    if gp.lockedm != 0 {
        // Hands off own p to the locked m,
        // then blocks waiting for a new p.
        startlockedm(gp)
        goto top
    }

    execute(gp, inheritTime)
}
```

m中的调度循环：
```
schedule --> execute
execute --> gogo
gogo --> goexit
goexit --> goexit1
goexit1 --> goexit0
goexit0 --> schedule
```

##### execute

```go
// Schedules gp to run on the current M.
// If inheritTime is true, gp inherits the remaining time in the
// current time slice. Otherwise, it starts a new time slice.
// Never returns.
//
// Write barriers are allowed because this is called immediately after
// acquiring a P in several places.
//
//go:yeswritebarrierrec
func execute(gp *g, inheritTime bool) {
    _g_ := getg() // 这个可能是 m 的 g0

    casgstatus(gp, _Grunnable, _Grunning)
    gp.waitsince = 0
    gp.preempt = false
    gp.stackguard0 = gp.stack.lo + _StackGuard
    if !inheritTime {
        _g_.m.p.ptr().schedtick++
    }
    _g_.m.curg = gp // 把当前 g 的位置让给 m
    gp.m = _g_.m // 把 gp 指向 m，建立双向关系

    gogo(&gp.sched)
}
```
绑定g和m，在gogo中运行g中的函数。

##### gogo

runtime.gogo是汇编写的，主要执行 `go func()`中的`func()`，将g对象中的gobuf传到寄存器中，然后从`gobuf.pc`开始执行。

```arm
// void gogo(Gobuf*)
// restore state from Gobuf; longjmp
TEXT runtime·gogo(SB), NOSPLIT, $16-8
    MOVQ    buf+0(FP), BX        // gobuf
    MOVQ    gobuf_g(BX), DX
    MOVQ    0(DX), CX        // make sure g != nil
    get_tls(CX)
    MOVQ    DX, g(CX)
    MOVQ    gobuf_sp(BX), SP    // restore SP
    MOVQ    gobuf_ret(BX), AX
    MOVQ    gobuf_ctxt(BX), DX
    MOVQ    gobuf_bp(BX), BP
    MOVQ    $0, gobuf_sp(BX)    // clear to help garbage collector
    MOVQ    $0, gobuf_ret(BX)
    MOVQ    $0, gobuf_ctxt(BX)
    MOVQ    $0, gobuf_bp(BX)
    MOVQ    gobuf_pc(BX), BX
    JMP    BX
```
其中`gobuf_sp(BX)`链接器会配合runtime将名字转换成偏移量，不然`gobuf_sp`只是一个`symbol`没有任何意义。
```
// The offsets of sp, pc, and g are known to (hard-coded in) libmach.
```

#### Goexit

Goexit:

```go
// Goexit terminates the goroutine that calls it. No other goroutine is affected.
// Goexit runs all deferred calls before terminating the goroutine. Because Goexit
// is not a panic, any recover calls in those deferred functions will return nil.
//
// Calling Goexit from the main goroutine terminates that goroutine
// without func main returning. Since func main has not returned,
// the program continues execution of other goroutines.
// If all other goroutines exit, the program crashes.
func Goexit() {
    // Run all deferred functions for the current goroutine.
    // This code is similar to gopanic, see that implementation
    // for detailed comments.
    gp := getg()
    for {
        d := gp._defer
        if d == nil {
            break
        }
        if d.started {
            if d._panic != nil {
                d._panic.aborted = true
                d._panic = nil
            }
            d.fn = nil
            gp._defer = d.link
            freedefer(d)
            continue
        }
        d.started = true
        reflectcall(nil, unsafe.Pointer(d.fn), deferArgs(d), uint32(d.siz), uint32(d.siz))
        if gp._defer != d {
            throw("bad defer entry in Goexit")
        }
        d._panic = nil
        d.fn = nil
        gp._defer = d.link
        freedefer(d)
        // Note: we ignore recovers here because Goexit isn't a panic
    }
    goexit1()
}

// Finishes execution of the current goroutine.
func goexit1() {
    if raceenabled {
        racegoend()
    }
    if trace.enabled {
        traceGoEnd()
    }
    mcall(goexit0)
}
```
```arm
// The top-most function running on a goroutine
// returns to goexit+PCQuantum.
TEXT runtime·goexit(SB),NOSPLIT,$0-0
    BYTE    $0x90    // NOP
    CALL    runtime·goexit1(SB)    // does not return
    // traceback from goexit1 must hit code range of goexit
    BYTE    $0x90    // NOP
```
mcall:
```arm
// func mcall(fn func(*g))
// Switch to m->g0's stack, call fn(g).
// Fn must never return. It should gogo(&g->sched)
// to keep running g.
TEXT runtime·mcall(SB), NOSPLIT, $0-8
    MOVQ    fn+0(FP), DI

    get_tls(CX)
    MOVQ    g(CX), AX    // save state in g->sched
    MOVQ    0(SP), BX    // caller's PC
    MOVQ    BX, (g_sched+gobuf_pc)(AX)
    LEAQ    fn+0(FP), BX    // caller's SP
    MOVQ    BX, (g_sched+gobuf_sp)(AX)
    MOVQ    AX, (g_sched+gobuf_g)(AX)
    MOVQ    BP, (g_sched+gobuf_bp)(AX)

    // switch to m->g0 & its stack, call fn
    MOVQ    g(CX), BX
    MOVQ    g_m(BX), BX
    MOVQ    m_g0(BX), SI
    CMPQ    SI, AX    // if g == m->g0 call badmcall
    JNE    3(PC)
    MOVQ    $runtime·badmcall(SB), AX
    JMP    AX
    MOVQ    SI, g(CX)    // g = m->g0
    MOVQ    (g_sched+gobuf_sp)(SI), SP    // sp = m->g0->sched.sp
    PUSHQ    AX
    MOVQ    DI, DX
    MOVQ    0(DI), DI
    CALL    DI
    POPQ    AX
    MOVQ    $runtime·badmcall2(SB), AX
    JMP    AX
    RET
```

wakep:
```go
// Tries to add one more P to execute G's.
// Called when a G is made runnable (newproc, ready).
func wakep() {
    // be conservative about spinning threads
    if !atomic.Cas(&sched.nmspinning, 0, 1) {
        return
    }
    startm(nil, true)
}

// Schedules some M to run the p (creates an M if necessary).
// If p==nil, tries to get an idle P, if no idle P's does nothing.
// May run with m.p==nil, so write barriers are not allowed.
// If spinning is set, the caller has incremented nmspinning and startm will
// either decrement nmspinning or set m.spinning in the newly started M.
//go:nowritebarrierrec
func startm(_p_ *p, spinning bool) {
    lock(&sched.lock)
    if _p_ == nil {
        _p_ = pidleget()
        if _p_ == nil {
             unlock(&sched.lock)
             if spinning {
                 // The caller incremented nmspinning, but there are no idle Ps,
                 // so it's okay to just undo the increment and give up.
                 if int32(atomic.Xadd(&sched.nmspinning, -1)) < 0 {
                     throw("startm: negative nmspinning")
                 }
             }
             return
        }
    }
    mp := mget()
    unlock(&sched.lock)
    if mp == nil {
        var fn func()
        if spinning {
            // The caller incremented nmspinning, so set m.spinning in the new M.
            fn = mspinning
        }
        newm(fn, _p_)
        return
    }
    if mp.spinning {
        throw("startm: m is spinning")
    }
    if mp.nextp != 0 {
        throw("startm: m has p")
    }
    if spinning && !runqempty(_p_) {
        throw("startm: p has runnable gs")
    }
    // The caller incremented nmspinning, so set m.spinning in the new M.
    mp.spinning = spinning
    mp.nextp.set(_p_)
    notewakeup(&mp.park)
}
```

### goroutine挂起

```go
// Puts the current goroutine into a waiting state and calls unlockf.
// If unlockf returns false, the goroutine is resumed.
// unlockf must not access this G's stack, as it may be moved between
// the call to gopark and the call to unlockf.
func gopark(unlockf func(*g, unsafe.Pointer) bool, lock unsafe.Pointer, reason string, traceEv byte, traceskip int) {
    mp := acquirem()
    gp := mp.curg
    status := readgstatus(gp)
    if status != _Grunning && status != _Gscanrunning {
        throw("gopark: bad g status")
    }
    mp.waitlock = lock
    mp.waitunlockf = *(*unsafe.Pointer)(unsafe.Pointer(&unlockf))
    gp.waitreason = reason
    mp.waittraceev = traceEv
    mp.waittraceskip = traceskip
    releasem(mp)
    // can't do anything that might move the G between Ms here.
    mcall(park_m)
}

func goready(gp *g, traceskip int) {
    systemstack(func() {
        ready(gp, traceskip, true)
    })
}

// Mark gp ready to run.
func ready(gp *g, traceskip int, next bool) {
    if trace.enabled {
        traceGoUnpark(gp, traceskip)
    }

    status := readgstatus(gp)

    // Mark runnable.
    _g_ := getg()
    _g_.m.locks++ // disable preemption because it can be holding p in a local var
    if status&^_Gscan != _Gwaiting {
        dumpgstatus(gp)
        throw("bad g->status in ready")
    }

    // status is Gwaiting or Gscanwaiting, make Grunnable and put on runq
    casgstatus(gp, _Gwaiting, _Grunnable)
    runqput(_g_.m.p.ptr(), gp, next)
    if atomic.Load(&sched.npidle) != 0 && atomic.Load(&sched.nmspinning) == 0 {
        wakep()
    }
    _g_.m.locks--
    if _g_.m.locks == 0 && _g_.preempt { // restore the preemption request in Case we've cleared it in newstack
        _g_.stackguard0 = stackPreempt
    }
}
```
```go
func notesleep(n *note) {
    gp := getg()
    if gp != gp.m.g0 {
        throw("notesleep not on g0")
    }
    ns := int64(-1)
    if *cgo_yield != nil {
        // Sleep for an arbitrary-but-moderate interval to poll libc interceptors.
        ns = 10e6
    }
    for atomic.Load(key32(&n.key)) == 0 {
        gp.m.blocked = true
        futexsleep(key32(&n.key), 0, ns)
        if *cgo_yield != nil {
            asmcgocall(*cgo_yield, nil)
        }
        gp.m.blocked = false
    }
}

// One-time notifications.
func noteclear(n *note) {
    n.key = 0
}

func notewakeup(n *note) {
    old := atomic.Xchg(key32(&n.key), 1)
    if old != 0 {
        print("notewakeup - double wakeup (", old, ")\n")
        throw("notewakeup - double wakeup")
    }
    futexwakeup(key32(&n.key), 1)
}
```

### findrunnable

流程(省略gc)：
```
runqget --> A[gp == nil]
A --> |no|return
A --> |yes|globrunqget
globrunqget --> B[gp == nil]
B --> |no| return
B --> |yes| C[netpollinited && lastpoll != 0]
C --> |yes|netpoll
netpoll --> K[gp == nil]
K --> |no|return
K --> |yes|runqsteal
C --> |no|runqsteal
runqsteal --> D[gp == nil]
D --> |no|return
D --> |yes|E[globrunqget]
E --> F[gp == nil]
F --> |no| return
F --> |yes| G[check all p's runq]
G --> H[runq is empty]
H --> |no|runqget
H --> |yes|I[netpoll]
I --> J[gp == nil]
J --> |no| return
J --> |yes| stopm
stopm --> runqget
```

```go
// 找到一个可执行的 goroutine 来 execute
// 会尝试从其它的 P 那里偷 g，从全局队列中拿，或者 network 中 poll
func findrunnable() (gp *g, inheritTime bool) {
    _g_ := getg()

    // The conditions here and in handoffp must agree: if
    // findrunnable would return a G to run, handoffp must start
    // an M.

top:
    _p_ := _g_.m.p.ptr()
    if sched.gcwaiting != 0 {
        gcstopm()
        goto top
    }
    if _p_.runSafePointFn != 0 {
        runSafePointFn()
    }
    if fingwait && fingwake {
        if gp := wakefing(); gp != nil {
            ready(gp, 0, true)
        }
    }
    if *cgo_yield != nil {
        asmcgocall(*cgo_yield, nil)
    }

    // 本地 runq
    if gp, inheritTime := runqget(_p_); gp != nil {
        return gp, inheritTime
    }

    // 全局 runq
    if sched.runqsize != 0 {
        lock(&sched.lock)
        gp := globrunqget(_p_, 0)
        unlock(&sched.lock)
        if gp != nil {
            return gp, false
        }
    }

    // Poll network.
    // netpoll 是我们执行 work-stealing 之前的一个优化
    // 如果没有任何的 netpoll 等待者，或者线程被阻塞在 netpoll 中，我们可以安全地跳过这段逻辑
    // 如果在阻塞的线程中存在任何逻辑上的竞争(e.g. 已经从 netpoll 中返回，但还没有设置 lastpoll)
    // 该线程还是会将下面的 netpoll 阻塞住
    if netpollinited() && atomic.Load(&netpollWaiters) > 0 && atomic.Load64(&sched.lastpoll) != 0 {
        if gp := netpoll(false); gp != nil { // 非阻塞
            // netpoll 返回 goroutine 链表，用 schedlink 连接
            injectglist(gp.schedlink.ptr())
            casgstatus(gp, _Gwaiting, _Grunnable)
            if trace.enabled {
                traceGoUnpark(gp, 0)
            }
            return gp, false
        }
    }

    // 从其它 p 那里偷 g
    procs := uint32(gomaxprocs)
    if atomic.Load(&sched.npidle) == procs-1 {
        // GOMAXPROCS=1 或者除了我们其它的 p 都是 idle
        // 新的工作可能从 syscall/cgocall，网络或者定时器中来。
        // 上面这些任务都不会被放到本地的 runq，所有没有可以 stealing 的点
        goto stop
    }
    // 如果正在自旋的 M 的数量 >= 忙着的 P，那么阻塞
    // 这是为了
    // 当 GOMAXPROCS 远大于 1，但程序的并行度又很低的时候
    // 防止过量的 CPU 消耗
    if !_g_.m.spinning && 2*atomic.Load(&sched.nmspinning) >= procs-atomic.Load(&sched.npidle) {
        goto stop
    }
    if !_g_.m.spinning {
        _g_.m.spinning = true
        atomic.Xadd(&sched.nmspinning, 1)
    }
    for i := 0; i < 4; i++ {
        for enum := stealOrder.start(fastrand()); !enum.done(); enum.next() {
            if sched.gcwaiting != 0 {
                goto top
            }
            stealRunNextG := i > 2 // first look for ready queues with more than 1 g
            if gp := runqsteal(_p_, allp[enum.position()], stealRunNextG); gp != nil {
                return gp, false
            }
        }
    }

stop:

    // 没有可以干的事情。如果我们正在 GC 的标记阶段，可以安全地扫描和加深对象的颜色，
    // 这样可以进行空闲时间的标记，而不是直接放弃 P
    if gcBlackenEnabled != 0 && _p_.gcBgMarkWorker != 0 && gcMarkWorkAvailable(_p_) {
        _p_.gcMarkWorkerMode = gcMarkWorkerIdleMode
        gp := _p_.gcBgMarkWorker.ptr()
        casgstatus(gp, _Gwaiting, _Grunnable)
        if trace.enabled {
            traceGoUnpark(gp, 0)
        }
        return gp, false
    }

    // Before we drop our P, make a snapshot of the allp slice,
    // which can change underfoot once we no longer block
    // safe-points. We don't need to snapshot the contents because
    // everything up to cap(allp) is immutable.
    allpSnapshot := allp

    // 返回 P 并阻塞
    lock(&sched.lock)
    if sched.gcwaiting != 0 || _p_.runSafePointFn != 0 {
        unlock(&sched.lock)
        goto top
    }
    if sched.runqsize != 0 {
        gp := globrunqget(_p_, 0)
        unlock(&sched.lock)
        return gp, false
    }
    if releasep() != _p_ {
        throw("findrunnable: wrong p")
    }
    pidleput(_p_)
    unlock(&sched.lock)

    // Delicate dance: thread transitions from spinning to non-spinning state,
    // potentially concurrently with submission of new goroutines. We must
    // drop nmspinning first and then check all per-P queues again (with
    // #StoreLoad memory barrier in between). If we do it the other way around,
    // another thread can submit a goroutine after we've checked all run queues
    // but before we drop nmspinning; as the result nobody will unpark a thread
    // to run the goroutine.
    // If we discover new work below, we need to restore m.spinning as a signal
    // for resetspinning to unpark a new worker thread (because there can be more
    // than one starving goroutine). However, if after discovering new work
    // we also observe no idle Ps, it is OK to just park the current thread:
    // the system is fully loaded so no spinning threads are required.
    // Also see "Worker thread parking/unparking" comment at the top of the file.
    wasSpinning := _g_.m.spinning
    if _g_.m.spinning {
        _g_.m.spinning = false
        if int32(atomic.Xadd(&sched.nmspinning, -1)) < 0 {
            throw("findrunnable: negative nmspinning")
        }
    }

    // 再检查一下所有的 runq
    for _, _p_ := range allpSnapshot {
        if !runqempty(_p_) {
            lock(&sched.lock)
            _p_ = pidleget()
            unlock(&sched.lock)
            if _p_ != nil {
                acquirep(_p_)
                if wasSpinning {
                    _g_.m.spinning = true
                    atomic.Xadd(&sched.nmspinning, 1)
                }
                goto top
            }
            break
        }
    }

    // 再检查 gc 空闲 g
    if gcBlackenEnabled != 0 && gcMarkWorkAvailable(nil) {
        lock(&sched.lock)
        _p_ = pidleget()
        if _p_ != nil && _p_.gcBgMarkWorker == 0 {
            pidleput(_p_)
            _p_ = nil
        }
        unlock(&sched.lock)
        if _p_ != nil {
            acquirep(_p_)
            if wasSpinning {
                _g_.m.spinning = true
                atomic.Xadd(&sched.nmspinning, 1)
            }
            // Go back to idle GC check.
            goto stop
        }
    }

    // poll network
    if netpollinited() && atomic.Load(&netpollWaiters) > 0 && atomic.Xchg64(&sched.lastpoll, 0) != 0 {
        if _g_.m.p != 0 {
            throw("findrunnable: netpoll with p")
        }
        if _g_.m.spinning {
            throw("findrunnable: netpoll with spinning")
        }
        gp := netpoll(true) // 阻塞到返回为止
        atomic.Store64(&sched.lastpoll, uint64(nanotime()))
        if gp != nil {
            lock(&sched.lock)
            _p_ = pidleget()
            unlock(&sched.lock)
            if _p_ != nil {
                acquirep(_p_)
                injectglist(gp.schedlink.ptr())
                casgstatus(gp, _Gwaiting, _Grunnable)
                if trace.enabled {
                    traceGoUnpark(gp, 0)
                }
                return gp, false
            }
            injectglist(gp)
        }
    }
    stopm()
    goto top
}
```

## m 和 p 解绑定

### handoffp
```
mexit --> A[is m0?]
A --> |yes|B[handoffp]
A --> |no| C[iterate allm]
C --> |m found|handoffp
C --> |m not found| throw

forEachP --> |p status == syscall| handoffp

stoplockedm --> handoffp

entersyscallblock --> entersyscallblock_handoff
entersyscallblock_handoff --> handoffp

retake --> |p status == syscall| handoffp
```
p会被放入全局空闲队列中
```go
// Hands off P from syscall or locked M.
// Always runs without a P, so write barriers are not allowed.
//go:nowritebarrierrec
func handoffp(_p_ *p) {
	// handoffp must start an M in any situation where
	// findrunnable would return a G to run on _p_.

	// if it has local work, start it straight away
	if !runqempty(_p_) || sched.runqsize != 0 {
		startm(_p_, false)
		return
	}
	// if it has GC work, start it straight away
	if gcBlackenEnabled != 0 && gcMarkWorkAvailable(_p_) {
		startm(_p_, false)
		return
	}
	// no local work, check that there are no spinning/idle M's,
	// otherwise our help is not required
	if atomic.Load(&sched.nmspinning)+atomic.Load(&sched.npidle) == 0 && atomic.Cas(&sched.nmspinning, 0, 1) { // TODO: fast atomic
		startm(_p_, true)
		return
	}
	lock(&sched.lock)
	if sched.gcwaiting != 0 {
		_p_.status = _Pgcstop
		sched.stopwait--
		if sched.stopwait == 0 {
			notewakeup(&sched.stopnote)
		}
		unlock(&sched.lock)
		return
	}
	if _p_.runSafePointFn != 0 && atomic.Cas(&_p_.runSafePointFn, 1, 0) {
		sched.safePointFn(_p_)
		sched.safePointWait--
		if sched.safePointWait == 0 {
			notewakeup(&sched.safePointNote)
		}
	}
	if sched.runqsize != 0 {
		unlock(&sched.lock)
		startm(_p_, false)
		return
	}
	// If this is the last running P and nobody is polling network,
	// need to wakeup another M to poll network.
	if sched.npidle == uint32(gomaxprocs-1) && atomic.Load64(&sched.lastpoll) != 0 {
		unlock(&sched.lock)
		startm(_p_, false)
		return
	}
	pidleput(_p_)
	unlock(&sched.lock)
}
```
## g 状态迁移

```
start{newg} --> Gidle
Gidle --> |oneNewExtraM|Gdead
Gidle --> |newproc1|Gdead

Gdead --> |newproc1|Grunnable
Gdead --> |needm|Gsyscall

Gscanrunning --> |scang|Grunning

Grunnable --> |execute|Grunning

Gany --> |casgcopystack|Gcopystack

Gcopystack --> |todotodo|Grunning

Gsyscall --> |dropm|Gdead
Gsyscall --> |exitsyscall0|Grunnable
Gsyscall --> |exitsyscall|Grunning

Grunning --> |goschedImpl|Grunnable
Grunning --> |goexit0|Gdead
Grunning --> |newstack|Gcopystack
Grunning --> |reentersyscall|Gsyscall
Grunning --> |entersyscallblock|Gsyscall
Grunning --> |markroot|Gwaiting
Grunning --> |gcAssistAlloc1|Gwaiting
Grunning --> |park_m|Gwaiting
Grunning --> |gcMarkTermination|Gwaiting
Grunning --> |gcBgMarkWorker|Gwaiting
Grunning --> |newstack|Gwaiting

Gwaiting --> |gcMarkTermination|Grunning
Gwaiting --> |gcBgMarkWorker|Grunning
Gwaiting --> |markroot|Grunning
Gwaiting --> |gcAssistAlloc1|Grunning
Gwaiting --> |newstack|Grunning
Gwaiting --> |findRunnableGCWorker|Grunnable
Gwaiting --> |ready|Grunnable
Gwaiting --> |findrunnable|Grunnable
Gwaiting --> |injectglist|Grunnable
Gwaiting --> |schedule|Grunnable
Gwaiting --> |park_m|Grunnable
Gwaiting --> |procresize|Grunnable
Gwaiting --> |checkdead|Grunnable
```
Gany表示任意状态

## p状态迁移

```
Pidle --> |acquirep1|Prunning

Psyscall --> |retake|Pidle
Psyscall --> |entersyscall_gcwait|Pgcstop
Psyscall --> |exitsyscallfast|Prunning

Pany --> |gcstopm|Pgcstop
Pany --> |forEachP|Pidle
Pany --> |releasep|Pidle
Pany --> |handoffp|Pgcstop
Pany --> |procresize release current p use allp 0|Pidle
Pany --> |procresize when init|Pgcstop
Pany --> |procresize when free old p| Pdead
Pany --> |procresize after resize use current p|Prunning
Pany --> |reentersyscall|Psyscall
Pany --> |stopTheWorldWithSema|Pgcstop
```
## 抢占流程

函数在goroutine栈上执行，因此执行期间可能会发生溢出，sp< stackguard0。代码来自go-internals
```arm
0x0000 TEXT    "".main(SB), $24-0
  ;; stack-split prologue
  0x0000 MOVQ    (TLS), CX
  0x0009 CMPQ    SP, 16(CX)
  0x000d JLS    58

  0x000f SUBQ    $24, SP
  0x0013 MOVQ    BP, 16(SP)
  0x0018 LEAQ    16(SP), BP
  ;; ...omitted FUNCDATA stuff...
  0x001d MOVQ    $137438953482, AX
  0x0027 MOVQ    AX, (SP)
  ;; ...omitted PCDATA stuff...
  0x002b CALL    "".add(SB)
  0x0030 MOVQ    16(SP), BP
  0x0035 ADDQ    $24, SP
  0x0039 RET

  ;; stack-split epilogue
  0x003a NOP
  ;; ...omitted PCDATA stuff...
  0x003a CALL    runtime.morestack_noctxt(SB)
  0x003f JMP    0
```
此处即对比sp与stackguard，JLS 表示 SP < 16(CX) 的话即跳转。
```arm
  ;; stack-split prologue
  0x0000 MOVQ    (TLS), CX
  0x0009 CMPQ    SP, 16(CX)
  0x000d JLS    58
```
这里因为 CX 寄存器存储的是 g 的起始地址，而 16(CX) 指的是 g 结构体偏移 16 个字节的位置，可以回顾一下 g 结构体定义，16 个字节恰好是跳过了第一个成员 stack(16字节) 之后的 stackguard0 的位置。

58转16进制为0x3a。
```arm
  ;; stack-split epilogue
  0x003a NOP
  ;; ...omitted PCDATA stuff...
  0x003a CALL    runtime.morestack_noctxt(SB)
  0x003f JMP    0
```

morestack_noctxt:
```arm
// morestack but not preserving ctxt.
TEXT runtime·morestack_noctxt(SB),NOSPLIT,$0
    MOVL    $0, DX
    JMP    runtime·morestack(SB)
```
morestack:
```arm
TEXT runtime·morestack(SB),NOSPLIT,$0-0
    // Cannot grow scheduler stack (m->g0).
    get_tls(CX)
    MOVQ    g(CX), BX
    MOVQ    g_m(BX), BX
    MOVQ    m_g0(BX), SI
    CMPQ    g(CX), SI
    JNE    3(PC)
    CALL    runtime·badmorestackg0(SB)
    INT    $3

    // Cannot grow signal stack (m->gsignal).
    MOVQ    m_gsignal(BX), SI
    CMPQ    g(CX), SI
    JNE    3(PC)
    CALL    runtime·badmorestackgsignal(SB)
    INT    $3

    // Called from f.
    // Set m->morebuf to f's caller.
    MOVQ    8(SP), AX    // f's caller's PC
    MOVQ    AX, (m_morebuf+gobuf_pc)(BX)
    LEAQ    16(SP), AX    // f's caller's SP
    MOVQ    AX, (m_morebuf+gobuf_sp)(BX)
    get_tls(CX)
    MOVQ    g(CX), SI
    MOVQ    SI, (m_morebuf+gobuf_g)(BX)

    // Set g->sched to context in f.
    MOVQ    0(SP), AX // f's PC
    MOVQ    AX, (g_sched+gobuf_pc)(SI)
    MOVQ    SI, (g_sched+gobuf_g)(SI)
    LEAQ    8(SP), AX // f's SP
    MOVQ    AX, (g_sched+gobuf_sp)(SI)
    MOVQ    BP, (g_sched+gobuf_bp)(SI)
    MOVQ    DX, (g_sched+gobuf_ctxt)(SI)

    // Call newstack on m->g0's stack.
    MOVQ    m_g0(BX), BX
    MOVQ    BX, g(CX)
    MOVQ    (g_sched+gobuf_sp)(BX), SP
    CALL    runtime·newstack(SB)
    MOVQ    $0, 0x1003    // crash if newstack returns
    RET
```

newstack:
```go
// Called from runtime·morestack when more stack is needed.
// Allocate larger stack and relocate to new stack.
// Stack growth is multiplicative, for constant amortized cost.
//
// g->atomicstatus will be Grunning or Gscanrunning upon entry.
// If the GC is trying to stop this g then it will set preemptscan to true.
//
// This must be nowritebarrierrec because it can be called as part of
// stack growth from other nowritebarrierrec functions, but the
// compiler doesn't check this.
//
//go:nowritebarrierrec
func newstack() {
    thisg := getg()
    // TODO: double check all gp. shouldn't be getg().
    if thisg.m.morebuf.g.ptr().stackguard0 == stackFork {
        throw("stack growth after fork")
    }
    if thisg.m.morebuf.g.ptr() != thisg.m.curg {
        print("runtime: newstack called from g=", hex(thisg.m.morebuf.g), "\n"+"\tm=", thisg.m, " m->curg=", thisg.m.curg, " m->g0=", thisg.m.g0, " m->gsignal=", thisg.m.gsignal, "\n")
        morebuf := thisg.m.morebuf
        traceback(morebuf.pc, morebuf.sp, morebuf.lr, morebuf.g.ptr())
        throw("runtime: wrong goroutine in newstack")
    }

    gp := thisg.m.curg

    if thisg.m.curg.throwsplit {
        // Update syscallsp, syscallpc in case traceback uses them.
        morebuf := thisg.m.morebuf
        gp.syscallsp = morebuf.sp
        gp.syscallpc = morebuf.pc
        pcname, pcoff := "(unknown)", uintptr(0)
        f := findfunc(gp.sched.pc)
        if f.valid() {
            pcname = funcname(f)
            pcoff = gp.sched.pc - f.entry
        }
        print("runtime: newstack at ", pcname, "+", hex(pcoff),
            " sp=", hex(gp.sched.sp), " stack=[", hex(gp.stack.lo), ", ", hex(gp.stack.hi), "]\n",
            "\tmorebuf={pc:", hex(morebuf.pc), " sp:", hex(morebuf.sp), " lr:", hex(morebuf.lr), "}\n",
            "\tsched={pc:", hex(gp.sched.pc), " sp:", hex(gp.sched.sp), " lr:", hex(gp.sched.lr), " ctxt:", gp.sched.ctxt, "}\n")

        thisg.m.traceback = 2 // Include runtime frames
        traceback(morebuf.pc, morebuf.sp, morebuf.lr, gp)
        throw("runtime: stack split at bad time")
    }

    morebuf := thisg.m.morebuf
    thisg.m.morebuf.pc = 0
    thisg.m.morebuf.lr = 0
    thisg.m.morebuf.sp = 0
    thisg.m.morebuf.g = 0

    // NOTE: stackguard0 may change underfoot, if another thread
    // is about to try to preempt gp. Read it just once and use that same
    // value now and below.
    preempt := atomic.Loaduintptr(&gp.stackguard0) == stackPreempt

    // Be conservative about where we preempt.
    // We are interested in preempting user Go code, not runtime code.
    // If we're holding locks, mallocing, or preemption is disabled, don't
    // preempt.
    // This check is very early in newstack so that even the status change
    // from Grunning to Gwaiting and back doesn't happen in this case.
    // That status change by itself can be viewed as a small preemption,
    // because the GC might change Gwaiting to Gscanwaiting, and then
    // this goroutine has to wait for the GC to finish before continuing.
    // If the GC is in some way dependent on this goroutine (for example,
    // it needs a lock held by the goroutine), that small preemption turns
    // into a real deadlock.
    if preempt {
        if thisg.m.locks != 0 || thisg.m.mallocing != 0 || thisg.m.preemptoff != "" || thisg.m.p.ptr().status != _Prunning {
            // Let the goroutine keep running for now.
            // gp->preempt is set, so it will be preempted next time.
            gp.stackguard0 = gp.stack.lo + _StackGuard
            gogo(&gp.sched) // never return
        }
    }

    if gp.stack.lo == 0 {
        throw("missing stack in newstack")
    }
    sp := gp.sched.sp
    if sys.ArchFamily == sys.AMD64 || sys.ArchFamily == sys.I386 {
        // The call to morestack cost a word.
        sp -= sys.PtrSize
    }
    if stackDebug >= 1 || sp < gp.stack.lo {
        print("runtime: newstack sp=", hex(sp), " stack=[", hex(gp.stack.lo), ", ", hex(gp.stack.hi), "]\n",
            "\tmorebuf={pc:", hex(morebuf.pc), " sp:", hex(morebuf.sp), " lr:", hex(morebuf.lr), "}\n",
            "\tsched={pc:", hex(gp.sched.pc), " sp:", hex(gp.sched.sp), " lr:", hex(gp.sched.lr), " ctxt:", gp.sched.ctxt, "}\n")
    }
    if sp < gp.stack.lo {
        print("runtime: gp=", gp, ", gp->status=", hex(readgstatus(gp)), "\n ")
        print("runtime: split stack overflow: ", hex(sp), " < ", hex(gp.stack.lo), "\n")
        throw("runtime: split stack overflow")
    }

    if preempt {
        if gp == thisg.m.g0 {
            throw("runtime: preempt g0")
        }
        if thisg.m.p == 0 && thisg.m.locks == 0 {
            throw("runtime: g is running but p is not")
        }
        // Synchronize with scang.
        casgstatus(gp, _Grunning, _Gwaiting)
        if gp.preemptscan {
            for !castogscanstatus(gp, _Gwaiting, _Gscanwaiting) {
                // Likely to be racing with the GC as
                // it sees a _Gwaiting and does the
                // stack scan. If so, gcworkdone will
                // be set and gcphasework will simply
                // return.
            }
            if !gp.gcscandone {
                // gcw is safe because we're on the
                // system stack.
                gcw := &gp.m.p.ptr().gcw
                scanstack(gp, gcw)
                if gcBlackenPromptly {
                    gcw.dispose()
                }
                gp.gcscandone = true
            }
            gp.preemptscan = false
            gp.preempt = false
            casfrom_Gscanstatus(gp, _Gscanwaiting, _Gwaiting)
            // This clears gcscanvalid.
            casgstatus(gp, _Gwaiting, _Grunning)
            gp.stackguard0 = gp.stack.lo + _StackGuard
            gogo(&gp.sched) // never return
        }

        // Act like goroutine called runtime.Gosched.
        casgstatus(gp, _Gwaiting, _Grunning)
        gopreempt_m(gp) // never return
    }

    // Allocate a bigger segment and move the stack.
    oldsize := gp.stack.hi - gp.stack.lo
    newsize := oldsize * 2
    if newsize > maxstacksize {
        print("runtime: goroutine stack exceeds ", maxstacksize, "-byte limit\n")
        throw("stack overflow")
    }

    // The goroutine must be executing in order to call newstack,
    // so it must be Grunning (or Gscanrunning).
    casgstatus(gp, _Grunning, _Gcopystack)

    // The concurrent GC will not scan the stack while we are doing the copy since
    // the gp is in a Gcopystack status.
    copystack(gp, newsize, true)
    if stackDebug >= 1 {
        print("stack grow done\n")
    }
    casgstatus(gp, _Gcopystack, _Grunning)
    gogo(&gp.sched)
}
```
流程：
```
start[entering func] --> cmp[sp < stackguard0]
cmp --> |yes| morestack_noctxt
cmp --> |no|final[execute func]
morestack_noctxt --> morestack
morestack --> newstack
newstack --> preempt
```
抢占在newstack中完成，修改抢占标志是在其他位置。

```
unlock --> |in case cleared in newstack|restorePreempt
ready --> |in case cleared in newstack|restorePreempt
startTheWorldWithSema --> |in case cleared in newstack|restorePreempt
allocm --> |in case cleared in newstack|restorePreempt
exitsyscall --> |in case cleared in newstack|restorePreempt
newproc1--> |in case cleared in newstack|restorePreempt
releasem -->  |in case cleared in newstack|restorePreempt

scang --> setPreempt
reentersyscall --> setPreempt
entersyscallblock --> setPreempt
preemptone--> setPreempt

enlistWorker --> preemptone
retake --> preemptone
preemptall --> preemptone
freezetheworld --> preemptall
stopTheWorldWithSema --> preemptall
forEachP --> preemptall
startpanic_m --> freezetheworld
gcMarkDone --> forEachP
```
只有gc与retake才会真正抢占g，其他地方只是恢复一下可能在newstack中被清除掉的抢占标记。

这里 entersyscall 和 entersyscallblock 比较特殊，虽然这俩函数的实现中有设置抢占标记，但实际上这两段逻辑是不会被走到的。因为 syscall 执行时是在 m 的 g0 栈上，如果在执行时被抢占，那么会直接 throw，而无法恢复。

