# redis事务实现
事务包括ACID特性，分别是Atomic（原子性）、Consistency（一致性）、Isolation（隔离性）和Durablity（持久性）。redis支持对一组操作事务化，要么全部成功，要么全部失败。

相关文件
- multi.c

## 概述

redis中对于事务的命令分别为
- MULTI：开始一个新事务
- DISCARD：放弃事务
- EXEC：执行事务中的所有命令
- WATCH：监听一个或多个key，如果有key在EXEC前被修改，则放弃事务
- UNWATCH：取消WATCH


## 数据结构

redis中每个client中都保存了一个事务状态属性，用来保存当前客户端中的事务。
```c
struct client{
    multiState mstate;
}
// 事务状态
typedef struct multiState {
    multiCmd *commands;     // 事务队列FIFO
    int count;              // 事务总数
    int cmd_flags;          // 命令flag
    int minreplicas;        // 最少备份数
    time_t minreplicas_timeout; // 超时时间
} multiState;

// 命令队列
typedef struct multiCmd {
    robj **argv; // 参数
    int argc; // 参数个数
    struct redisCommand *cmd; // 命令
} multiCmd;

```

## 事务操作

```c
// 添加命令到队列中
void queueMultiCommand(client *c) {
    multiCmd *mc;
    int j;

    // 重新申请内存，大小+1
    c->mstate.commands = zrealloc(c->mstate.commands,
            sizeof(multiCmd)*(c->mstate.count+1));
    // 修改偏移量，指向新申请的地址
    mc = c->mstate.commands+c->mstate.count;
    mc->cmd = c->cmd;
    mc->argc = c->argc;
    mc->argv = zmalloc(sizeof(robj*)*c->argc);
    memcpy(mc->argv,c->argv,sizeof(robj*)*c->argc);
    for (j = 0; j < c->argc; j++)
        incrRefCount(mc->argv[j]);
    c->mstate.count++;
    c->mstate.cmd_flags |= c->cmd->flags;
}

// watch一个key
void watchCommand(client *c) {
    int j;

    if (c->flags & CLIENT_MULTI) {
        addReplyError(c,"WATCH inside MULTI is not allowed");
        return;
    }
    for (j = 1; j < c->argc; j++)
        watchForKey(c,c->argv[j]);
    addReply(c,shared.ok);
}

void watchForKey(client *c, robj *key) {
    list *clients = NULL;
    listIter li;
    listNode *ln;
    watchedKey *wk;

    // 检查client是否已经watch这个key了
    listRewind(c->watched_keys,&li);
    while((ln = listNext(&li))) {
        wk = listNodeValue(ln);
        if (wk->db == c->db && equalStringObjects(key,wk->key))
            return; /* Key already watched */
    }
    // 拿出这个key对应的client列表
    clients = dictFetchValue(c->db->watched_keys,key);
    if (!clients) {
        clients = listCreate();
        dictAdd(c->db->watched_keys,key,clients);
        incrRefCount(key);
    }
    listAddNodeTail(clients,c);
    // 将新的client放进去
    wk = zmalloc(sizeof(*wk));
    wk->key = key;
    wk->db = c->db;
    incrRefCount(key);
    listAddNodeTail(c->watched_keys,wk);
}

// touch会将watch指定key的client修改为CLIENT_DIRTY_CAS，client下一次EXEC将会失败
void touchWatchedKey(redisDb *db, robj *key)

// 会将当前数据库所有的client中watch key存在的修改为CLIENT_DIRTY_CAS
void touchWatchedKeysOnFlush(int dbid)
```

添加完指令之后，这一个事务中的指令会同时成功或同时失败。

```c
void execCommand(client *c) {
    int j;
    robj **orig_argv;
    int orig_argc;
    struct redisCommand *orig_cmd;
    int must_propagate = 0; // 是否需要同步到AOF/Slaves
    int was_master = server.masterhost == NULL;

    if (!(c->flags & CLIENT_MULTI)) {
        addReplyError(c,"EXEC without MULTI");
        return;
    }

    // 监控的key被修改或者添加命令队列出错都会终止事务
    // 添加命令队列出错会返回错误
    if (c->flags & (CLIENT_DIRTY_CAS|CLIENT_DIRTY_EXEC)) {
        addReply(c, c->flags & CLIENT_DIRTY_EXEC ? shared.execaborterr :
                                                   shared.nullarray[c->resp]);
        // 终止事务
        discardTransaction(c);
        goto handle_monitor;
    }

    // 只读节点上进行写命令，终止
    // 启动后配置变更会发生
    if (!server.loading && server.masterhost && server.repl_slave_ro &&
        !(c->flags & CLIENT_MASTER) && c->mstate.cmd_flags & CMD_WRITE)
    {
        addReplyError(c,
            "Transaction contains write commands but instance "
            "is now a read-only replica. EXEC aborted.");
        discardTransaction(c);
        goto handle_monitor;
    }

    /* Exec all the queued commands */
    unwatchAllKeys(c); /* Unwatch ASAP otherwise we'll waste CPU cycles */
    orig_argv = c->argv;
    orig_argc = c->argc;
    orig_cmd = c->cmd;
    addReplyArrayLen(c,c->mstate.count);
    for (j = 0; j < c->mstate.count; j++) {
        c->argc = c->mstate.commands[j].argc;
        c->argv = c->mstate.commands[j].argv;
        c->cmd = c->mstate.commands[j].cmd;

        // 写命令以及管理命令会进行传播，事务作为一个整体
        if (!must_propagate &&
            !server.loading &&
            !(c->cmd->flags & (CMD_READONLY|CMD_ADMIN)))
        {
            execCommandPropagateMulti(c);
            must_propagate = 1;
        }

        int acl_keypos;
        int acl_retval = ACLCheckCommandPerm(c,&acl_keypos);
        if (acl_retval != ACL_OK) {
            addACLLogEntry(c,acl_retval,acl_keypos,NULL);
            addReplyErrorFormat(c,
                "-NOPERM ACLs rules changed between the moment the "
                "transaction was accumulated and the EXEC call. "
                "This command is no longer allowed for the "
                "following reason: %s",
                (acl_retval == ACL_DENIED_CMD) ?
                "no permission to execute the command or subcommand" :
                "no permission to touch the specified keys");
        } else {
            // 执行命令
            call(c,server.loading ? CMD_CALL_NONE : CMD_CALL_FULL);
        }

        /* Commands may alter argc/argv, restore mstate. */
        c->mstate.commands[j].argc = c->argc;
        c->mstate.commands[j].argv = c->argv;
        c->mstate.commands[j].cmd = c->cmd;
    }
    // 恢复命令
    c->argv = orig_argv;
    c->argc = orig_argc;
    c->cmd = orig_cmd;
    // 取消事务状态
    discardTransaction(c);

    // 确保事务被传播了
    if (must_propagate) {
        int is_master = server.masterhost == NULL;
        server.dirty++;
        // 确保master->slave之后意外终止的事务binlog正常
        if (server.repl_backlog && was_master && !is_master) {
            char *execcmd = "*1\r\n$4\r\nEXEC\r\n";
            feedReplicationBacklog(execcmd,strlen(execcmd));
        }
    }

handle_monitor:
    /* Send EXEC to clients waiting data from MONITOR. We do it here
     * since the natural order of commands execution is actually:
     * MUTLI, EXEC, ... commands inside transaction ...
     * Instead EXEC is flagged as CMD_SKIP_MONITOR in the command
     * table, and we do it here with correct ordering. */
    if (listLength(server.monitors) && !server.loading)
        replicationFeedMonitors(c,server.monitors,c->db->id,c->argv,c->argc);
}
```

取消事务比较简单，释放命令结构体重新分配，重置flag，并取消watch的key，但是并不会回滚。

```c
void discardTransaction(client *c) {
    freeClientMultiState(c);
    initClientMultiState(c);
    c->flags &= ~(CLIENT_MULTI|CLIENT_DIRTY_CAS|CLIENT_DIRTY_EXEC);
    unwatchAllKeys(c);
}

void freeClientMultiState(client *c) {
    int j;

    for (j = 0; j < c->mstate.count; j++) {
        int i;
        multiCmd *mc = c->mstate.commands+j;

        for (i = 0; i < mc->argc; i++)
            decrRefCount(mc->argv[i]);
        zfree(mc->argv);
    }
    zfree(c->mstate.commands);
}
void initClientMultiState(client *c) {
    c->mstate.commands = NULL;
    c->mstate.count = 0;
    c->mstate.cmd_flags = 0;
}
```

## 总结

redis中事务会一起执行，但并不支持回滚，watch操作属于乐观锁，被修改了则取消事务执行。