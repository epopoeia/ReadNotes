# redis网络层实现

网络层负责数据的收发，也是I/O多路复用实现的一部分。同时事件机制来对socket进行监控接受链接，并在建立连接之后监控所有的socket来读取数据。

## 服务启动

redis启动过程为
- socket：建立套接字
- bind：绑定端口
- listen：启动监听
- non_block：设置为非阻塞
- createvent：将socket读事件注册到epoll上
- epoll：事件循环获取准备好的读事件

```
socket->bind->listen->accept
```


```c
void initServer(void) {
    // 监听端口
    if (server.port != 0 &&
        listenToPort(server.port,server.ipfd,&server.ipfd_count) == C_ERR)
        exit(1);
    // 注册socket读事件，事件循环器中会处理
   for (j = 0; j < server.ipfd_count; j++) {
        if (aeCreateFileEvent(server.el, server.ipfd[j], AE_READABLE,
            acceptTcpHandler,NULL) == AE_ERR)
            {
                serverPanic(
                    "Unrecoverable error creating server.ipfd file event.");
            }
    }
}

// 端口有链接即可读，事件循环会触发处理函数
void acceptTcpHandler(aeEventLoop *el, int fd, void *privdata, int mask) {
    int cport, cfd, max = MAX_ACCEPTS_PER_CALL;
    char cip[NET_IP_STR_LEN];
    UNUSED(el);
    UNUSED(mask);
    UNUSED(privdata);

    // 一次最多处理max个连接，防止阻塞
    while(max--) {
        // 使用accept接受链接，会生成一个文件描述符
        cfd = anetTcpAccept(server.neterr, fd, cip, sizeof(cip), &cport);
        if (cfd == ANET_ERR) {
            if (errno != EWOULDBLOCK)
                serverLog(LL_WARNING,
                    "Accepting client connection: %s", server.neterr);
            return;
        }
        serverLog(LL_VERBOSE,"Accepted %s:%d", cip, cport);
        // 使用上面的fd创建一个客户端，并加入到eventloop中
        // 包括设置非阻塞与nodelay以及保活，并注册读函数
        acceptCommonHandler(connCreateAcceptedSocket(cfd),0,cip);
    }
}
```

注册连接之后，当连接可读事件触发之后，将会调用之前注册的读函数，开始处理连接对应客户端的数据。

```c
// 读取数据，如果开启了多线程仅加入队列
// 每次最多读取16KB保存到querybuf中
void readQueryFromClient(connection *conn)

// 读取querybuf中的数据，解析成字符串数组argv，然后调用执行函数，执行命令
void processInputBuffer(client *c)

// 执行命令并重置客户端
int processCommandAndResetClient(client *c)
// 执行命令函数
int processCommand(client *c) {
    moduleCallCommandFilters(c);

    // quit命令会立刻返回给客户端
    if (!strcasecmp(c->argv[0]->ptr,"quit")) {
        addReply(c,shared.ok);
        c->flags |= CLIENT_CLOSE_AFTER_REPLY;
        return C_ERR;
    }

    // 查找命令，命令硬编码，初始化时写入字典
    c->cmd = c->lastcmd = lookupCommand(c->argv[0]->ptr);
    if (!c->cmd) {
        // 没找到，返回，有错时都要调用这个如果启用了事务标记DIRTY_EXEC，后面的就会失败
        flagTransaction(c);
        sds args = sdsempty();
        int i;
        for (i=1; i < c->argc && sdslen(args) < 128; i++)
            args = sdscatprintf(args, "`%.*s`, ", 128-(int)sdslen(args), (char*)c->argv[i]->ptr);
        addReplyErrorFormat(c,"unknown command `%s`, with args beginning with: %s",
            (char*)c->argv[0]->ptr, args);
        sdsfree(args);
        return C_OK;
    } else if ((c->cmd->arity > 0 && c->cmd->arity != c->argc) ||
               (c->argc < -c->cmd->arity)) {
        flagTransaction(c);
        addReplyErrorFormat(c,"wrong number of arguments for '%s' command",
            c->cmd->name);
        return C_OK;
    }

    // 是否需要登陆
    int auth_required = (!(DefaultUser->flags & USER_FLAG_NOPASS) ||
                          (DefaultUser->flags & USER_FLAG_DISABLED)) &&
                        !c->authenticated;
    if (auth_required) {
        /* AUTH and HELLO and no auth modules are valid even in
         * non-authenticated state. */
        if (!(c->cmd->flags & CMD_NO_AUTH)) {
            flagTransaction(c);
            addReply(c,shared.noautherr);
            return C_OK;
        }
    }

    // ACL检查
    int acl_keypos;
    int acl_retval = ACLCheckCommandPerm(c,&acl_keypos);
    if (acl_retval != ACL_OK) {
        addACLLogEntry(c,acl_retval,acl_keypos,NULL);
        flagTransaction(c);
        if (acl_retval == ACL_DENIED_CMD)
            addReplyErrorFormat(c,
                "-NOPERM this user has no permissions to run "
                "the '%s' command or its subcommand", c->cmd->name);
        else
            addReplyErrorFormat(c,
                "-NOPERM this user has no permissions to access "
                "one of the keys used as arguments");
        return C_OK;
    }

    // 集群模式是否需要重定向
    // 发送者时master或者命令没有key则不重定向
    if (server.cluster_enabled &&
        !(c->flags & CLIENT_MASTER) &&
        !(c->flags & CLIENT_LUA &&
          server.lua_caller->flags & CLIENT_MASTER) &&
        !(c->cmd->getkeys_proc == NULL && c->cmd->firstkey == 0 &&
          c->cmd->proc != execCommand))
    {
        int hashslot;
        int error_code;
        // 寻找key对应的slot，根据hash来判断
        // 如果slot不存在直接返回
        // slot存在，并且不在迁移，也不在导入；并且多个key要么相同，要么属于同一个slot，返回自己
        // 如果迁移或者导入命令，返回自己
        // 如果迁移并且出发了missingkey，返回ASK，问迁移后的节点
        // 作为被ask的节点。并且为迁移的导入节点，多key，并且missing，返回unstable
        clusterNode *n = getNodeByQuery(c,c->cmd,c->argv,c->argc,
                                        &hashslot,&error_code);
        if (n == NULL || n != server.cluster->myself) {
            // 找到的节点不是自己
            if (c->cmd->proc == execCommand) {
                // exec对应的是事务的命令，要抛弃事务
                discardTransaction(c);
            } else {
                flagTransaction(c);
            }
            // 根据错误码让客户端重定向或者做其他处理
            clusterRedirectClient(c,n,hashslot,error_code);
            return C_OK;
        }
    }

    // 存在忙lua脚本不重新分配内存
    if (server.maxmemory && !server.lua_timedout) {
        int out_of_memory = freeMemoryIfNeededAndSafe() == C_ERR;
        /* freeMemoryIfNeeded may flush slave output buffers. This may result
         * into a slave, that may be the active client, to be freed. */
        if (server.current_client == NULL) return C_ERR;

        /* It was impossible to free enough memory, and the command the client
         * is trying to execute is denied during OOM conditions or the client
         * is in MULTI/EXEC context? Error. */
        if (out_of_memory &&
            (c->cmd->flags & CMD_DENYOOM ||
             (c->flags & CLIENT_MULTI &&
              c->cmd->proc != execCommand &&
              c->cmd->proc != discardCommand)))
        {
            flagTransaction(c);
            addReply(c, shared.oomerr);
            return C_OK;
        }

        /* Save out_of_memory result at script start, otherwise if we check OOM
         * untill first write within script, memory used by lua stack and
         * arguments might interfere. */
        if (c->cmd->proc == evalCommand || c->cmd->proc == evalShaCommand) {
            server.lua_oom = out_of_memory;
        }
    }

    /* Make sure to use a reasonable amount of memory for client side
     * caching metadata. */
    if (server.tracking_clients) trackingLimitUsedSlots();

    // 如果不能持久化，不能保存写数据
    int deny_write_type = writeCommandsDeniedByDiskError();
    if (deny_write_type != DISK_ERROR_TYPE_NONE &&
        server.masterhost == NULL &&
        (c->cmd->flags & CMD_WRITE ||
         c->cmd->proc == pingCommand))
    {
        flagTransaction(c);
        if (deny_write_type == DISK_ERROR_TYPE_RDB)
            addReply(c, shared.bgsaveerr);
        else
            addReplySds(c,
                sdscatprintf(sdsempty(),
                "-MISCONF Errors writing to the AOF file: %s\r\n",
                strerror(server.aof_last_write_errno)));
        return C_OK;
    }

    // 没有足够的从节点备份
    if (server.masterhost == NULL &&
        server.repl_min_slaves_to_write &&
        server.repl_min_slaves_max_lag &&
        c->cmd->flags & CMD_WRITE &&
        server.repl_good_slaves_count < server.repl_min_slaves_to_write)
    {
        flagTransaction(c);
        addReply(c, shared.noreplicaserr);
        return C_OK;
    }

    // 只读从节点不接受写命令
    if (server.masterhost && server.repl_slave_ro &&
        !(c->flags & CLIENT_MASTER) &&
        c->cmd->flags & CMD_WRITE)
    {
        flagTransaction(c);
        addReply(c, shared.roslaveerr);
        return C_OK;
    }

    /* Only allow a subset of commands in the context of Pub/Sub if the
     * connection is in RESP2 mode. With RESP3 there are no limits. */
    if ((c->flags & CLIENT_PUBSUB && c->resp == 2) &&
        c->cmd->proc != pingCommand &&
        c->cmd->proc != subscribeCommand &&
        c->cmd->proc != unsubscribeCommand &&
        c->cmd->proc != psubscribeCommand &&
        c->cmd->proc != punsubscribeCommand) {
        addReplyErrorFormat(c,
            "Can't execute '%s': only (P)SUBSCRIBE / "
            "(P)UNSUBSCRIBE / PING / QUIT are allowed in this context",
            c->cmd->name);
        return C_OK;
    }

    /* Only allow commands with flag "t", such as INFO, SLAVEOF and so on,
     * when slave-serve-stale-data is no and we are a slave with a broken
     * link with master. */
    if (server.masterhost && server.repl_state != REPL_STATE_CONNECTED &&
        server.repl_serve_stale_data == 0 &&
        !(c->cmd->flags & CMD_STALE))
    {
        flagTransaction(c);
        addReply(c, shared.masterdownerr);
        return C_OK;
    }

    /* Loading DB? Return an error if the command has not the
     * CMD_LOADING flag. */
    if (server.loading && !(c->cmd->flags & CMD_LOADING)) {
        addReply(c, shared.loadingerr);
        return C_OK;
    }

    /* Lua script too slow? Only allow a limited number of commands.
     * Note that we need to allow the transactions commands, otherwise clients
     * sending a transaction with pipelining without error checking, may have
     * the MULTI plus a few initial commands refused, then the timeout
     * condition resolves, and the bottom-half of the transaction gets
     * executed, see Github PR #7022. */
    if (server.lua_timedout &&
          c->cmd->proc != authCommand &&
          c->cmd->proc != helloCommand &&
          c->cmd->proc != replconfCommand &&
          c->cmd->proc != multiCommand &&
          c->cmd->proc != execCommand &&
          c->cmd->proc != discardCommand &&
          c->cmd->proc != watchCommand &&
          c->cmd->proc != unwatchCommand &&
        !(c->cmd->proc == shutdownCommand &&
          c->argc == 2 &&
          tolower(((char*)c->argv[1]->ptr)[0]) == 'n') &&
        !(c->cmd->proc == scriptCommand &&
          c->argc == 2 &&
          tolower(((char*)c->argv[1]->ptr)[0]) == 'k'))
    {
        flagTransaction(c);
        addReply(c, shared.slowscripterr);
        return C_OK;
    }

    /* Exec the command */
    if (c->flags & CLIENT_MULTI &&
        c->cmd->proc != execCommand && c->cmd->proc != discardCommand &&
        c->cmd->proc != multiCommand && c->cmd->proc != watchCommand)
    {
        queueMultiCommand(c);
        addReply(c,shared.queued);
    } else {
        // 执行命令
        call(c,CMD_CALL_FULL);
        c->woff = server.master_repl_offset;
        if (listLength(server.ready_keys))
            handleClientsBlockedOnKeys();
    }
    return C_OK;
}
```

call函数为真正执行命令的函数，根据不同的flag会采取不同的行为比如同步到AOF或者slave，或者不同步
```c
void call(client *c, int flags) {
    long long dirty;
    ustime_t start, duration;
    int client_old_flags = c->flags;
    struct redisCommand *real_cmd = c->cmd;

    server.fixed_time_expire++;

    // monitor模式返回处理的每一个指令
    if (listLength(server.monitors) &&
        !server.loading &&
        !(c->cmd->flags & (CMD_SKIP_MONITOR|CMD_ADMIN)))
    {
        replicationFeedMonitors(c,server.monitors,c->db->id,c->argv,c->argc);
    }

    /* Initialization: clear the flags that must be set by the command on
     * demand, and initialize the array for additional commands propagation. */
    c->flags &= ~(CLIENT_FORCE_AOF|CLIENT_FORCE_REPL|CLIENT_PREVENT_PROP);
    redisOpArray prev_also_propagate = server.also_propagate;
    redisOpArrayInit(&server.also_propagate);

    // 处理指令
    dirty = server.dirty;
    updateCachedTime(0);
    start = server.ustime;
    // 指令的处理函数
    c->cmd->proc(c);
    duration = ustime()-start;
    dirty = server.dirty-dirty;
    if (dirty < 0) dirty = 0;

    /* When EVAL is called loading the AOF we don't want commands called
     * from Lua to go into the slowlog or to populate statistics. */
    if (server.loading && c->flags & CLIENT_LUA)
        flags &= ~(CMD_CALL_SLOWLOG | CMD_CALL_STATS);

    /* If the caller is Lua, we want to force the EVAL caller to propagate
     * the script if the command flag or client flag are forcing the
     * propagation. */
    if (c->flags & CLIENT_LUA && server.lua_caller) {
        if (c->flags & CLIENT_FORCE_REPL)
            server.lua_caller->flags |= CLIENT_FORCE_REPL;
        if (c->flags & CLIENT_FORCE_AOF)
            server.lua_caller->flags |= CLIENT_FORCE_AOF;
    }

    // 使用slowlog
    if (flags & CMD_CALL_SLOWLOG && !(c->cmd->flags & CMD_SKIP_SLOWLOG)) {
        char *latency_event = (c->cmd->flags & CMD_FAST) ?
                              "fast-command" : "command";
        latencyAddSampleIfNeeded(latency_event,duration/1000);
        slowlogPushEntryIfNeeded(c,c->argv,c->argc,duration);
    }

    if (flags & CMD_CALL_STATS) {
        /* use the real command that was executed (cmd and lastamc) may be
         * different, in case of MULTI-EXEC or re-written commands such as
         * EXPIRE, GEOADD, etc. */
        real_cmd->microseconds += duration;
        real_cmd->calls++;
    }

    // 同步到AOF或者replication
    if (flags & CMD_CALL_PROPAGATE &&
        (c->flags & CLIENT_PREVENT_PROP) != CLIENT_PREVENT_PROP)
    {
        int propagate_flags = PROPAGATE_NONE;

        // 检查命令是否修改了数据，修改了则进行同步
        if (dirty) propagate_flags |= (PROPAGATE_AOF|PROPAGATE_REPL);

        // 强制同步标记
        if (c->flags & CLIENT_FORCE_REPL) propagate_flags |= PROPAGATE_REPL;
        if (c->flags & CLIENT_FORCE_AOF) propagate_flags |= PROPAGATE_AOF;

        // 设置了阻止同步标记
        if (c->flags & CLIENT_PREVENT_REPL_PROP ||
            !(flags & CMD_CALL_PROPAGATE_REPL))
                propagate_flags &= ~PROPAGATE_REPL;
        if (c->flags & CLIENT_PREVENT_AOF_PROP ||
            !(flags & CMD_CALL_PROPAGATE_AOF))
                propagate_flags &= ~PROPAGATE_AOF;

        // 有一个可以传播的就传播
        if (propagate_flags != PROPAGATE_NONE && !(c->cmd->flags & CMD_MODULE))
            propagate(c->cmd,c->db->id,c->argv,c->argc,propagate_flags);
    }

    // 恢复flag，call递归调用
    c->flags &= ~(CLIENT_FORCE_AOF|CLIENT_FORCE_REPL|CLIENT_PREVENT_PROP);
    c->flags |= client_old_flags &
        (CLIENT_FORCE_AOF|CLIENT_FORCE_REPL|CLIENT_PREVENT_PROP);

    // 调用使用alsoPropagate()添加的方法，alsoPropagate()会将方法添加到server.also_propagate中
    if (server.also_propagate.numops) {
        int j;
        redisOp *rop;

        if (flags & CMD_CALL_PROPAGATE) {
            int multi_emitted = 0;
            /* Wrap the commands in server.also_propagate array,
             * but don't wrap it if we are already in MULTI context,
             * in case the nested MULTI/EXEC.
             *
             * And if the array contains only one command, no need to
             * wrap it, since the single command is atomic. */
            if (server.also_propagate.numops > 1 &&
                !(c->cmd->flags & CMD_MODULE) &&
                !(c->flags & CLIENT_MULTI) &&
                !(flags & CMD_CALL_NOWRAP))
            {
                // 传播事务
                execCommandPropagateMulti(c);
                multi_emitted = 1;
            }

            for (j = 0; j < server.also_propagate.numops; j++) {
                rop = &server.also_propagate.ops[j];
                int target = rop->target;
                /* Whatever the command wish is, we honor the call() flags. */
                if (!(flags&CMD_CALL_PROPAGATE_AOF)) target &= ~PROPAGATE_AOF;
                if (!(flags&CMD_CALL_PROPAGATE_REPL)) target &= ~PROPAGATE_REPL;
                if (target)
                    propagate(rop->cmd,rop->dbid,rop->argv,rop->argc,target);
            }

            if (multi_emitted) {
                // 最后要传播exec命令
                execCommandPropagateExec(c);
            }
        }
        redisOpArrayFree(&server.also_propagate);
    }
    server.also_propagate = prev_also_propagate;

    /* If the client has keys tracking enabled for client side caching,
     * make sure to remember the keys it fetched via this command. */
    if (c->cmd->flags & CMD_READONLY) {
        client *caller = (c->flags & CLIENT_LUA && server.lua_caller) ?
                            server.lua_caller : c;
        if (caller->flags & CLIENT_TRACKING &&
            !(caller->flags & CLIENT_TRACKING_BCAST))
        {
            trackingRememberKeys(caller);
        }
    }

    server.fixed_time_expire--;
    server.stat_numcommands++;
}
```

以上即为命令执行的主要过程，有点偏题了

## anet封装
ant为redsi对底层tcp链接的封装，将监听地址以及读写封装为API方便上层系统调用。API列表如下
```c
// tcp链接，默认是阻塞的
int anetTcpConnect(char *err, const char *addr, int port);

// tcp非阻塞链接
int anetTcpNonBlockConnect(char *err, const char *addr, int port);

// tcp服务器非阻塞bind
int anetTcpNonBlockBindConnect(char *err, const char *addr, int port, const char *source_addr);

// tcp服务器非阻塞链接并且失败时会重连
int anetTcpNonBlockBestEffortBindConnect(char *err, const char *addr, int port, const char *source_addr);

// 本地套接字阻塞链接
int anetUnixConnect(char *err, const char *path);

// 本地套接字非阻塞链接
int anetUnixNonBlockConnect(char *err, const char *path);

// 从套接字中读取count个字符保存到buf中
int anetRead(int fd, char *buf, int count);

// 处理所有主机地址为host或者ip
int anetResolve(char *err, char *host, char *ipbuf, size_t ipbuf_len);

// 只处理地址为ip的
int anetResolveIP(char *err, char *host, char *ipbuf, size_t ipbuf_len);

// tcp使用ipv4或ipv6的IP协议创建server
int anetTcpServer(char *err, int port, char *bindaddr, int backlog);

// 创建tcp6的server
int anetTcp6Server(char *err, int port, char *bindaddr, int backlog);

// unix本地文件服务器套接字处理
int anetUnixServer(char *err, char *path, mode_t perm, int backlog);

// 接受tcp链接
int anetTcpAccept(char *err, int serversock, char *ip, size_t ip_len, int *port);

// 接受unix本地套接字链接
int anetUnixAccept(char *err, int serversock);

// 写入数据
int anetWrite(int fd, char *buf, int count);

// 设置描述符为非阻塞还是阻塞
int anetNonBlock(char *err, int fd);
int anetBlock(char *err, int fd);

// 是否关闭Nagle算法，立即发送
int anetEnableTcpNoDelay(char *err, int fd);
int anetDisableTcpNoDelay(char *err, int fd);

// tcp保活
int anetTcpKeepAlive(char *err, int fd);

// 数据发送超时
int anetSendTimeout(char *err, int fd, long long ms);

// 数据接收超时
int anetRecvTimeout(char *err, int fd, long long ms);

// 根据套接字返回对方ip及端口号
int anetPeerToString(int fd, char *ip, size_t ip_len, int *port);

// 设置tcp保活检测对方主机是否挂掉
int anetKeepAlive(char *err, int fd, int interval);

// 根据套接字描述符获取自己的ip地址以及端口号
int anetSockName(int fd, char *ip, size_t ip_len, int *port);

// 格式化地址以及端口输出
int anetFormatAddr(char *fmt, size_t fmt_len, char *ip, int port);

// 传入套接字描述符，返回对方格式化后的端口以及地址字符串
int anetFormatPeer(int fd, char *fmt, size_t fmt_len);

// 根据套接字返回自己的IP地址几端口号
int anetFormatSock(int fd, char *fmt, size_t fmt_len);
```

## networking
networking为redis中逻辑网络层，实现对客户端连接的处理。

### 客户端连接
客户端连接事件在eventloop中被监测到之后会调用acceptTcpHandler建立tcp连接，之后使用acceptCommonHandler在server中建立client并保存ip地址以及数据缓冲区等。

### 客户端的创建与释放
接受连接之后会初始化client结构体，并将client放入sever中的相应结构中进行保存。销毁客户端通过unlinkClient函数移除客户端的可见引用(不包括Pub/Sub)。

1. 首先会从服务器链表中删除客户端，并且移除与client相关的事件并删除待写以及阻塞队列。
2. 然后会释放client空间，如果连接的是master暂不释放空间，因为master需要检查客户端状态；与从服务器断开，释放查询语句缓冲区，解开阻塞，删除阻塞字典，unwatch所有的key，退订所有频道与模式，释放reply结构体，移除所有引用；从服务器的客户端断开连接，主节点需要更新从节点存活状态；主服务器的客户端断开连接单独处理与主节点断开连接。
3. 异步释放，将客户端加入到异步释放队列中为防止正在往客户端写数据，异步释放会在每次循环中处理。

### 回写客户端
接受客户端请求在上面的query中写了，处理完之后要回写客户端。每次事件循环中在等待系统epoll返回之前都会调用beforeSleep函数，这里会调用handleClientsWithPendingReadsUsingThreads读取pending中的client并执行命令，也会handleClientsWithPendingWritesUsingThreads对需要回复的客户端进行回写。

在redis6中增加了io线程池，所以对以前的操作进行了一些更改来避免竞争态。

```c
int handleClientsWithPendingWritesUsingThreads(void) {
    int processed = listLength(server.clients_pending_write);
    if (processed == 0) return 0; /* Return ASAP if there are no clients. */

    // 单线程调用下列函数
    if (server.io_threads_num == 1 || stopThreadedIOIfNeeded()) {
        return handleClientsWithPendingWrites();
    }

    /* Start threads if needed. */
    if (!io_threads_active) startThreadedIO();

    if (tio_debug) printf("%d TOTAL WRITE pending clients\n", processed);

    // 将client分到不同的线程中处理
    listIter li;
    listNode *ln;
    listRewind(server.clients_pending_write,&li);
    int item_id = 0;
    while((ln = listNext(&li))) {
        client *c = listNodeValue(ln);
        c->flags &= ~CLIENT_PENDING_WRITE;
        int target_id = item_id % server.io_threads_num;
        listAddNodeTail(io_threads_list[target_id],c);
        item_id++;
    }

    // 通知线程池开始处理
    io_threads_op = IO_THREADS_OP_WRITE;
    for (int j = 1; j < server.io_threads_num; j++) {
        int count = listLength(io_threads_list[j]);
        io_threads_pending[j] = count;
    }

    // 主线程同样处理数据
    listRewind(io_threads_list[0],&li);
    while((ln = listNext(&li))) {
        client *c = listNodeValue(ln);
        // 写到客户端，0是为了避免竞争后面会写
        writeToClient(c,0);
    }
    listEmpty(io_threads_list[0]);

    // 等待所有线程完成
    while(1) {
        unsigned long pending = 0;
        for (int j = 1; j < server.io_threads_num; j++)
            pending += io_threads_pending[j];
        if (pending == 0) break;
    }
    if (tio_debug) printf("I/O WRITE All threads finshed\n");

    // 有一些没写完的client，要安装write handler
    // 因为有最大程度限制等
    listRewind(server.clients_pending_write,&li);
    while((ln = listNext(&li))) {
        client *c = listNodeValue(ln);

        /* Install the write handler if there are pending writes in some
         * of the clients. */
         // 这里只有单线程了，所以不会产生竞争
         // 创建writeable事件，会在epoll中准备好之后继续写，写完要删掉event
        if (clientHasPendingReplies(c) &&
                connSetWriteHandler(c->conn, sendReplyToClient) == AE_ERR)
        {
            freeClientAsync(c);
        }
    }
    listEmpty(server.clients_pending_write);
    return processed;
}
```

```c
// 单线程状态下的写处理
int handleClientsWithPendingWrites(void) {
    listIter li;
    listNode *ln;
    int processed = listLength(server.clients_pending_write);

    listRewind(server.clients_pending_write,&li);
    // 遍历pending的client列表
    while((ln = listNext(&li))) {
        client *c = listNodeValue(ln);
        c->flags &= ~CLIENT_PENDING_WRITE;
        // 拿出来一个
        listDelNode(server.clients_pending_write,ln);

        /* If a client is protected, don't do anything,
         * that may trigger write error or recreate handler. */
        if (c->flags & CLIENT_PROTECTED) continue;

        // 向socket中写入
        if (writeToClient(c,0) == C_ERR) continue;

        // 上面同步写之后还有数据
        // 因为写入有最大限制，为了保证其他客户端响应速度
        if (clientHasPendingReplies(c)) {
            int ae_barrier = 0;
            // fsycn=always模式下，不允许读出现在写之前
            // 但是beforesleep先调用的读，所以不让写
            if (server.aof_state == AOF_ON &&
                server.aof_fsync == AOF_FSYNC_ALWAYS)
            {
                ae_barrier = 1;
            }
            // 设置屏障
            // 用处是先持久化，下一次eventloop在发送到客户端
            if (connSetWriteHandlerWithBarrier(c->conn, sendReplyToClient, ae_barrier) == C_ERR) {
                freeClientAsync(c);
            }
        }
    }
    return processed;
}
```

```c
// 写入客户端socket
int writeToClient(client *c, int handler_installed) {
    ssize_t nwritten = 0, totwritten = 0;
    size_t objlen;
    clientReplyBlock *o;

    // 客户端有数据，写入
    while(clientHasPendingReplies(c)) {
        if (c->bufpos > 0) {
            nwritten = connWrite(c->conn,c->buf+c->sentlen,c->bufpos-c->sentlen);
            if (nwritten <= 0) break;
            c->sentlen += nwritten;
            totwritten += nwritten;

            // 一次性写完
            if ((int)c->sentlen == c->bufpos) {
                c->bufpos = 0;
                c->sentlen = 0;
            }
        } else {
            o = listNodeValue(listFirst(c->reply));
            objlen = o->used;

            if (objlen == 0) {
                c->reply_bytes -= o->size;
                listDelNode(c->reply,listFirst(c->reply));
                continue;
            }

            nwritten = connWrite(c->conn, o->buf + c->sentlen, objlen - c->sentlen);
            if (nwritten <= 0) break;
            c->sentlen += nwritten;
            totwritten += nwritten;

            /* If we fully sent the object on head go to the next one */
            if (c->sentlen == objlen) {
                c->reply_bytes -= o->size;
                listDelNode(c->reply,listFirst(c->reply));
                c->sentlen = 0;
                /* If there are no longer objects in the list, we expect
                 * the count of reply bytes to be exactly zero. */
                if (listLength(c->reply) == 0)
                    serverAssert(c->reply_bytes == 0);
            }
        }
        // 写入大小限制，但是从节点没有
        if (totwritten > NET_MAX_WRITES_PER_EVENT &&
            (server.maxmemory == 0 ||
             zmalloc_used_memory() < server.maxmemory) &&
            !(c->flags & CLIENT_SLAVE)) break;
    }
    server.stat_net_output_bytes += totwritten;
    if (nwritten == -1) {
        if (connGetState(c->conn) == CONN_STATE_CONNECTED) {
            nwritten = 0;
        } else {
            serverLog(LL_VERBOSE,
                "Error writing to client: %s", connGetLastError(c->conn));
            freeClientAsync(c);
            return C_ERR;
        }
    }
    if (totwritten > 0) {
        // 更新交互时间，来自master的同步，不算交互
        if (!(c->flags & CLIENT_MASTER)) c->lastinteraction = server.unixtime;
    }
    if (!clientHasPendingReplies(c)) {
        c->sentlen = 0;
        // 仍然没写完
        if (handler_installed) connSetWriteHandler(c->conn, NULL);

        /* Close connection after entire reply has been sent. */
        if (c->flags & CLIENT_CLOSE_AFTER_REPLY) {
            freeClientAsync(c);
            return C_ERR;
        }
    }
    return C_OK;
}
```

所有需要写入客户端的数据会提前调用addReply函数，将数据写入client的缓冲区中。