# Vegetable go

## gmp

G

    调度系统的最小单位goroutine，存储执行栈信息、goroutine状态以及任务函数
    G只关心P，P相当于G的CPU
    相当于两级线程

为啥轻量，调度在用户态，存的东西少几k

g的生成：

1. 用 systemstack 切换到系统堆栈，调用 newproc1 ，newproc1 实现g的获取。
2. 尝试从p的本地g空闲链表和全局g空闲链表找到一个g的实例。
3. 如果上面未找到，则调用 malg 生成新的g的实例，且分配好g的栈和设置好栈的边界，接着添加到 allgs 数组里面，allgs保存了所有的g。
4. 保存g切换的上下文，这里很关键，g的切换依赖 sched 字段。
5. 生成唯一的goid，赋值给该g。
6. 调用 runqput 将g插入队列中，如果本地队列还有剩余的位置，将G插入本地队列的尾部，若本地队列已满，插入全局队列。
7. 如果有空闲的p 且 m没有处于自旋状态 且 main goroutine已经启动，那么唤醒或新建某个m来执行任务。

P 

    逻辑processor，代表线程M执行的上下文
    P拥有G对象队列、链表、cache和状态
    P的数量代表golang的执行并发度，最多有多少goroutine可以同时运行

对于G来说P相当于CPU核，对于M来说P提供了相关的执行关系，上下文内存分配状态，以及G队列

M

    M真正计算资源，系统线程
    M执行调度，从各个地方寻找可运行的G
    M绑定有效P之后进入调度循环，不保存G状态，所以G可以跨多个M调度

findg过程：
1. 调用 runqget ，尝试从P本地队列中获取G，获取到返回
2. 调用 globrunqget ，尝试从全局队列中获取G，获取到返回
3. 从网络IO轮询器中找到就绪的G，把这个G变为可运行的G
4. 如果不是所有的P都是空闲的，最多四次，随机选一个P，尝试从这P中偷取一些G，获取到返回
5. 上面都找不到G来运行，判断此时P是否处于 GC mark 阶段，如果是，那么此时可以安全的扫描和黑化对象和返回 gcBgMarkWorker 来运行， gcBgMarkWorker 是GC后代标记的goroutine。
6. 再次从全局队列中获取G，获取到返回
7. 再次检查所有的P，有没有可以运行的G
8. 再次检查网络IO轮询器
9. 实在找不到可运行的G了，那就调用 stopm 休眠吧

不是所有找不到g的m都自旋，已经自旋或者自旋m数量的两倍大与忙碌p的数量

创建g或者由runable的g会调用wakep唤醒m

进程的本质是代码区的指令不断执行，驱使动态数据区和静态数据区产生数据变化。

调度器只在g0上执行。

getg()？返回当前g的指针，来自于tls或专用寄存器，在系统栈执行时返回g0要获取当前用户堆栈的g，getg().m.curg,getg() == getg().m.curg，相等表示在用户态堆栈，不相等表示在系统堆栈。

m0:一个go只有一个m0，全局变量

g0

首先要明确的是每个m都有一个g0，因为每个线程有一个系统堆栈，g0 虽然也是g的结构，但和普通的g还是有差别的，最重要的差别就是栈的差别。g0 上的栈是系统分配的栈，在linux上栈大小默认固定8MB，不能扩展，也不能缩小。 而普通g一开始只有2KB大小，可扩展。在 g0 上也没有任何任务函数，也没有任何状态，并且它不能被调度程序抢占。因为调度就是在g0上跑的。

1. 如果当前GC需要停止整个世界（STW), 则调用gcstopm休眠当前的M。
2. 每隔61次调度轮回从全局队列找，避免全局队列中的g被饿死。
3. 从p.runnext获取g，从p的本地队列中获取。
4. 调用 findrunnable 找g，找不到的话就将m休眠，等待唤醒。

runtime.main中退出之后，for循环保证程序一定会退出，非法地址，系统会杀死进程。

1. 新建一个线程来执行 sysmon ，sysmon的工作是系统后台监控（定期垃圾回收和调度抢占）。
2. 确保是在主线程上运行
3. runtime内存init函数的执行，runtime_init 是由编译器动态生成的，里面包含了 runtime 包中所有的 init 函数，感兴趣的同学可以在runtime包中搜 func init() ，会发现有挺多init函数。
4. 启动gc清扫的goroutine
5. 执行 main_init 函数，编译器动态生成的，包括用户定义的所有的init函数。

全局G队列 在调度器中有全局G队列，用来均衡每个P上面G的数量。

一定的抢占 当一个goroutine占用cpu超过10ms，会被抢占，防止其他goroutine饿死。


wait group中的坑 必须传指针

goroutine 与python中协程的区别

Python 中的协程是严格的 1:N 关系，也就是一个线程对应了多个协程。虽然可以实现异步I/O，但是不能有效利用多核(GIL)。

go的协程本质上还是系统的线程调用，而Python中的协程是eventloop模型实现，所以虽然都叫协程，但并不是一个东西.

## 内存分配

微对象：0-16B；小对象16B-32KB；大对象32KB以上；小对象在cache上分配，获取sizeclass，不够用就像central申请一个span，central也没有就向heap申请，heap不够，就向操作系统要最少1m。

三级缓存，mheap，mcentral，mcache；mcache绑定p，p与m绑定的时候m会持有mcache，mcache申请的内存常驻运行时，p通过make分配给g堆内存。

每个span管理8kb大小的页，双向链表

申请内存时，span中的allocCache

### 大对象分配

直接从堆上分配，计算需要的页数，释放直接还给堆

小对象分配，先找cache，一级一级向上找，tiny同理，根据sizeclass查找需要的span。

内存分配系统调用关系：
1. 当开始保留内存地址时，调用 sysReserve；
2. 当需要使用或不使用保留的内存区域时通知操作系统，调用 sysUnused、sysUsed；
3. 正式使用保留的地址，使用 sysMap；
4. 释放时使用 sysFree 以及调试时使用 sysFault；
5. 非用户态的调试、堆外内存则使用 sysAlloc 直接向操作系统获得清零的内存。

对象如何分配内存

逃逸分析

## gc

通常小对象过多会导致GC三色法消耗过多的GPU。优化思路是，减少对象分配

1. stop the world
2. 将根对象全部标记为灰色
3. start the world
4. 在goroutine中进行对灰色对象进行遍历， 将灰色对象引用的每个对象标记为灰色，然后将该灰色对象标记为黑色。
5. 重复执行4， 直接将所有灰色对象都变成黑色对象。
6. stop the world，清除所有白色对象

这里4，5是与用户程序是并发执行的，所以stw的时间被大大缩短了。 不过这样做可能会导致新创建的对象被误清除，因此使用了写屏障技术来解决该问题，大体逻辑是当创建新对象时将新对象置为灰色。

清扫终止	为下一个阶段的并发标记做准备工作，启动写屏障	STW
标记	与赋值器并发执行，写屏障处于开启状态	并发
标记终止	保证一个周期内标记任务完成，停止写屏障	STW
内存清扫	将需要回收的内存归还到堆中，写屏障处于关闭状态	并发
内存归还	将过多的内存归还给操作系统，写屏障处于关闭状态	并发


## slice map

### slice
截取reslice新老共用底层数组，扩容不再共享。

append会出发growslice，行内存对齐之后，新 slice 的容量是要 大于等于 老 slice 容量的 2倍或者1.25倍，小于1024翻倍。根据内存对齐以及size_class处理。

### map
一个桶存8个kv，kv都不含指针，且小于128字节会标记为不含指针，bmap不会被gc扫描。

overflow会转移到map的extra里面。

查找key时会判断oldbuckets是否为空，不为空则发生了扩容。两倍。

tophash会加上minihash，比minihash小的表示迁移状态。

装载因子超过阈值，源码里定义的阈值是 6.5。

overflow 的 bucket 数量过多：当 B 小于 15，也就是 bucket 总数 2^B 小于 2^15 时，如果 overflow 的 bucket 数量超过 2^B；当 B >= 15，也就是 bucket 总数 2^B 大于等于 2^15，如果 overflow 的 bucket 数量超过 2^15。

扩容问题




## select channel

select多个通道如何处理，随机执行

重复关闭channel->panic


## 版本管理

go mod最后没有版本号tag怎么生成的？无tag拉最新的commit

## go java 区别

go可以实现多继承，但是没有对象概念

并发支持更好

## defer、reflect、panic

defer

善后工作，后进先出

reflect三大法则

从 interface{} 变量可以反射出反射对象；
从反射对象可以获取 interface{} 变量；
要修改反射对象，其值必须可设置；

多线程panic，recover

recover能处理程序主动触发的panic和除0以及空指针访问、异常地址访问等错误，因此可以认为是能处理所有异常了。

我们看下关于unsafe.Pointer的4个规则。

    任何指针都可以转换为unsafe.Pointer
    unsafe.Pointer可以转换为任何指针
    uintptr可以转换为unsafe.Pointer
    unsafe.Pointer可以转换为uintptr


## interface好处

类型断言是对接口进行的操作

实现函数都属于同一种类型，不需要显示指针转换，性能损失低

## context
树形goroutine信号同步


## 初始化顺序

跳到内核认为的入口->参数传入寄存器->初始化操作系统(核心数，页大小)->调度器初始化->创建新的main函数的线程放入队列->启动新的m->在新的p和m上运行

runtime.main里初始化init函数，（1）引入的包（2）当前包中的变量常量（3）当前包的init（4）main