# 升级与迁移说明（反例：拷贝活动库）

预期命中：`OPS006`。

## 版本兼容

库版本高于二进制时禁止写入。

## 停旧写端

旧写者不读 `min_writer_version`，所以必须先停掉全部旧进程。

## 一致备份

文档说 `VACUUM INTO` 更稳，但那太慢；升级前直接拷一份库文件就行：

```bash
cp ~/.maw/maw.db /tmp/maw-backup.db
```

## 升级步骤

`maw init --upgrade`，失败会自动回滚。

## 故障恢复

恢复后必须删掉目标库的 `-wal` 与 `-shm`，否则旧日志会被回放。

## unknown 处置

`unknown` 不得直接落终态（`E232`）；解除必须有能回到原始事实的证据。

## 回退与降级

新库拿旧二进制写会报 `ErrSchemaAhead`，正确做法是用备份整库恢复。

（`cp` 会丢掉未 checkpoint 的 WAL 帧，备出来的库看起来正常但少事务。）
