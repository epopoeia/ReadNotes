# redis事件实现-ae

redis中采用Reactor模式处理事件，所有的事件会放出事件池，redis依次从池中取出事件进行处理。

redis中的事件主要分为时间事件(逻辑时钟)以及文件事件(I/O)，文件事件采用I/O多路复用技术，监听感兴趣的I/O事件，当文件描述符准备好时将事件放入待处理事件池，在事件循环时将进行处理。

时间事件为内部定时器，满足时间要求将事件标记为待处理，文件事件优先级高于时间事件。


## 数据结构

```c
#define AE_NONE 0       // 不需要事件
#define AE_READABLE 1   // 描述符可读时触发
#define AE_WRITABLE 2   // 描述符可写时触发
#define AE_BARRIER 4    // 优先处理可写事件
// 文件事件
typedef struct aeFileEvent {
    int mask; /* one of AE_(READABLE|WRITABLE|BARRIER) */
    aeFileProc *rfileProc;  // 读处理函数
    aeFileProc *wfileProc;  // 写处理函数
    void *clientData;    // 事件数据
} aeFileEvent;

// 时间事件
typedef struct aeTimeEvent {
    long long id; // 时间事件标识符
    long when_sec; // 触发时间 秒
    long when_ms; // 触发时间微秒
    aeTimeProc *timeProc;  // 处理函数
    aeEventFinalizerProc *finalizerProc; // 销毁函数，删除时调用
    void *clientData;  // 数据
    struct aeTimeEvent *prev;
    struct aeTimeEvent *next;
    int refcount; // 防止销毁递归调用的时间事件
} aeTimeEvent;

// 待触发事件
typedef struct aeFiredEvent {
    int fd;   // 文件描述符
    int mask; // 读写标记见上
} aeFiredEvent;

// 事件循环
typedef struct aeEventLoop {
    int maxfd;   // 当前注册的最大描述符
    int setsize; // 监听描述符数量
    long long timeEventNextId;  // 下一个时间事件的id
    time_t lastTime;     // 上次事件循环的时间，用于检测系统时间是否变更
    aeFileEvent *events; // 注册要使用的文件事件
    aeFiredEvent *fired; // 待触发事件
    aeTimeEvent *timeEventHead; // 时间事件头部，链表结构
    int stop; // 1 表示停止
    void *apidata; // 处理底层特定的API数据，epoll中的epoll_fd,epoll_event
    aeBeforeSleepProc *beforesleep;  // 阻塞之前调用
    aeBeforeSleepProc *aftersleep;   // 系统调用返回之后调用
    int flags;
} aeEventLoop;
```

## 事件操作

```c
// 创建事件循环，分配指定大小的空间并置零，apidata会初始化为相应操作系统的多路复用接口
aeEventLoop *aeCreateEventLoop(int setsize);

// 释放所有空间与系统调用
void aeDeleteEventLoop(aeEventLoop *eventLoop);

// stop标志置1
void aeStop(aeEventLoop *eventLoop);

// 将相应的处理函数与事件相关联，并加入到操作系统的事件池中
int aeCreateFileEvent(aeEventLoop *eventLoop, int fd, int mask,
        aeFileProc *proc, void *clientData);

// 从操作系统池删除事件，但并不会释放空间，重置mask
void aeDeleteFileEvent(aeEventLoop *eventLoop, int fd, int mask);

// 根据文件描述符获取上面的事件
int aeGetFileEvents(aeEventLoop *eventLoop, int fd);

// 创建时间事件，插入到链表头部
long long aeCreateTimeEvent(aeEventLoop *eventLoop, long long milliseconds,
        aeTimeProc *proc, void *clientData,
        aeEventFinalizerProc *finalizerProc);

// 删除需要遍历，把id设为-1
int aeDeleteTimeEvent(aeEventLoop *eventLoop, long long id);

// process传入的flag
#define AE_FILE_EVENTS (1<<0)  // 处理文件事件
#define AE_TIME_EVENTS (1<<1)  // 处理时间事件
#define AE_ALL_EVENTS (AE_FILE_EVENTS|AE_TIME_EVENTS)  // 处理全部
#define AE_DONT_WAIT (1<<2)    // 待处理事件处理完后立刻返回，不等待(超时)
#define AE_CALL_BEFORE_SLEEP (1<<3) // before sleep回调
#define AE_CALL_AFTER_SLEEP (1<<4)  // after sleep回调
// 返回处理的事件的数量
// 根据是否有超时时间调用系统调用获取可触发事件，依次触发
// 文件事件触发完成后，处理时间事件，依次遍历删除掉标记为删除的事件，如果系统时间被往前修改过，又改回来了，那么时间事件会在本次调度立刻触发
// 实践表明早触发比晚触发风险低
int aeProcessEvents(aeEventLoop *eventLoop, int flags) {
    int processed = 0, numevents;

    // 直接返回
    if (!(flags & AE_TIME_EVENTS) && !(flags & AE_FILE_EVENTS)) return 0;

    // 没有文件事件，只想处理时间事件也需要阻塞到下一个时间事件准备好为止
    if (eventLoop->maxfd != -1 ||
        ((flags & AE_TIME_EVENTS) && !(flags & AE_DONT_WAIT))) {
        int j;
        aeTimeEvent *shortest = NULL;
        struct timeval tv, *tvp;

        // 最近的时间事件，遍历
        if (flags & AE_TIME_EVENTS && !(flags & AE_DONT_WAIT))
            shortest = aeSearchNearestTimer(eventLoop);
        if (shortest) {
            long now_sec, now_ms;

            aeGetTime(&now_sec, &now_ms);
            tvp = &tv;

            // 计算时间事件还有多久触发，用来设置文件事件阻塞超时时间
            long long ms =
                (shortest->when_sec - now_sec)*1000 +
                shortest->when_ms - now_ms;

            if (ms > 0) {
                tvp->tv_sec = ms/1000;
                tvp->tv_usec = (ms % 1000)*1000;
            } else {
                // 立刻处理
                tvp->tv_sec = 0;
                tvp->tv_usec = 0;
            }
        } else {
            // 没有待处理的时间事件，检查是否需要设置超时时间
            if (flags & AE_DONT_WAIT) {
                tv.tv_sec = tv.tv_usec = 0;
                tvp = &tv;
            } else {
                /* Otherwise we can block */
                tvp = NULL; /* wait forever */
            }
        }

        if (eventLoop->flags & AE_DONT_WAIT) {
            tv.tv_sec = tv.tv_usec = 0;
            tvp = &tv;
        }

        if (eventLoop->beforesleep != NULL && flags & AE_CALL_BEFORE_SLEEP)
            eventLoop->beforesleep(eventLoop);

        // I/O复用获取准备好的文件事件
        numevents = aeApiPoll(eventLoop, tvp);

        /* After sleep callback. */
        if (eventLoop->aftersleep != NULL && flags & AE_CALL_AFTER_SLEEP)
            eventLoop->aftersleep(eventLoop);

        // 开始处理文件事件
        for (j = 0; j < numevents; j++) {
            aeFileEvent *fe = &eventLoop->events[eventLoop->fired[j].fd];
            int mask = eventLoop->fired[j].mask;
            int fd = eventLoop->fired[j].fd;
            int fired = 0; // 当前描述符上事件触发次数

            // 通常先读后写，返回数据比较快
            // 设置了AE_BARRIER，先写后读
            int invert = fe->mask & AE_BARRIER;

            if (!invert && fe->mask & mask & AE_READABLE) {
                fe->rfileProc(eventLoop,fd,fe->clientData,mask);
                fired++;
                fe = &eventLoop->events[fd]; /* Refresh in case of resize. */
            }

            /* Fire the writable event. */
            if (fe->mask & mask & AE_WRITABLE) {
                if (!fired || fe->wfileProc != fe->rfileProc) {
                    fe->wfileProc(eventLoop,fd,fe->clientData,mask);
                    fired++;
                }
            }

            // AE_BARRIER设置了才用
            if (invert) {
                fe = &eventLoop->events[fd]; /* Refresh in case of resize. */
                if ((fe->mask & mask & AE_READABLE) &&
                    (!fired || fe->wfileProc != fe->rfileProc))
                {
                    fe->rfileProc(eventLoop,fd,fe->clientData,mask);
                    fired++;
                }
            }

            processed++;
        }
    }
    // 看看是否处理时间事件
    if (flags & AE_TIME_EVENTS)
        processed += processTimeEvents(eventLoop);

    return processed; // 返回处理事件数量
}

// 使用系统调用，等待文件变为指定状态，milliseconds超时时间
int aeWait(int fd, int mask, long long milliseconds);

// 事件循环主函数，重复调用process
void aeMain(aeEventLoop *eventLoop);

// 操作系统API名字
char *aeGetApiName(void);

// 设置系统调用前执行的函数，会做一些前期处理，比如处理阻塞客户端
void aeSetBeforeSleepProc(aeEventLoop *eventLoop, aeBeforeSleepProc *beforesleep);

// 系统调用结束之后调用
void aeSetAfterSleepProc(aeEventLoop *eventLoop, aeBeforeSleepProc *aftersleep);

// 返回时间队列大小
int aeGetSetSize(aeEventLoop *eventLoop);

// 调用系统层resize
int aeResizeSetSize(aeEventLoop *eventLoop, int setsize);

// 设置下一次事件循环的超时时间
void aeSetDontWait(aeEventLoop *eventLoop, int noWait);
```

在服务器启动时会创建文件事件以及时间事件，对于文件事件分为客户端连接事件以及客户端读写事件，时间事件只有一个逻辑时钟。

```c
void initServer(void) {
    
    // 创建指定大小的事件循环
    server.el = aeCreateEventLoop(server.maxclients+CONFIG_FDSET_INCR);

    // 创建时间事件，逻辑时钟
    if (aeCreateTimeEvent(server.el, 1, serverCron, NULL, NULL) == AE_ERR) {
        serverPanic("Can't create event loop timers.");
        exit(1);
    }

    // 创建文件事件用于接受连接
    for (j = 0; j < server.ipfd_count; j++) {
        if (aeCreateFileEvent(server.el, server.ipfd[j], AE_READABLE,
            acceptTcpHandler,NULL) == AE_ERR)
            {
                serverPanic(
                    "Unrecoverable error creating server.ipfd file event.");
            }
    }
    for (j = 0; j < server.tlsfd_count; j++) {
        if (aeCreateFileEvent(server.el, server.tlsfd[j], AE_READABLE,
            acceptTLSHandler,NULL) == AE_ERR)
            {
                serverPanic(
                    "Unrecoverable error creating server.tlsfd file event.");
            }
    }
}

// 连接建立成功之后创建写事件
static int connSocketConnect(connection *conn, const char *addr, int port, const char *src_addr,
        ConnectionCallbackFunc connect_handler){
                aeCreateFileEvent(server.el, conn->fd, AE_WRITABLE,
            conn->type->ae_handler, conn);
        }
```

## I/O多路复用

多路复用通过监控多个文件描述符返回准备好的文件描述符给调用者进行操作。不同操作系统提供的接口不同。
```c
#ifdef HAVE_EVPORT
#include "ae_evport.c"
#else
    #ifdef HAVE_EPOLL
    #include "ae_epoll.c"
    #else
        #ifdef HAVE_KQUEUE
        #include "ae_kqueue.c"
        #else
        #include "ae_select.c"
        #endif
    #endif
#endif
```

多路复用统一函数定义
```c
// 添加需要监听的事件
static int aeApiAddEvent(aeEventLoop *eventLoop, int fd, int mask)
// 初始化I/O多路复用库所需的参数
static int aeApiCreate(aeEventLoop *eventLoop)
// 删除不需要监听的事件
static void aeApiDelEvent(aeEventLoop *eventLoop, int fd, int mask)
// 销毁
static void aeApiFree(aeEventLoop *eventLoop)
// 返回底层API的名字
static int aeApiName(void)
// 取出已准备好的文件描述符，kqueue实现
static int aeApiPoll(aeEventLoop *eventLoop, struct timeval *tvp) {
    aeApiState *state = eventLoop->apidata;
    int retval, numevents = 0;

    // 是否有超时时间
    if (tvp != NULL) {
        struct timespec timeout;
        timeout.tv_sec = tvp->tv_sec;
        timeout.tv_nsec = tvp->tv_usec * 1000;
        retval = kevent(state->kqfd, NULL, 0, state->events, eventLoop->setsize,
                        &timeout);
    } else {
        retval = kevent(state->kqfd, NULL, 0, state->events, eventLoop->setsize,
                        NULL);
    }
    // 准备好的事件数
    if (retval > 0) {
        int j;

        numevents = retval;
        // 遍历修改事件的标识并返回
        for(j = 0; j < numevents; j++) {
            int mask = 0;
            struct kevent *e = state->events+j;

            if (e->filter == EVFILT_READ) mask |= AE_READABLE;
            if (e->filter == EVFILT_WRITE) mask |= AE_WRITABLE;
            eventLoop->fired[j].fd = e->ident;
            eventLoop->fired[j].mask = mask;
        }
    }
    return numevents;
}
```

## 总结

在redis6中引入了多线程I/O，在beforesleep函数中，会处理pending client，这时如果启用了多线程，将会通过多线程加速处理，对于事件循环过程中依旧是单线程处理每一个事件。