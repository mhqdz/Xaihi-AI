"""测试数据辅助：自建会话、跑完删干净。

两条规矩，都是从实际事故里来的：

1. 会发消息的用例必须自己造会话。曾经 robustness.py 直接拿 `?conv=118` 当靶子，
   把 `rb-test` 和「（已停止）」写进了用户的真实对话。
2. 清理要真删，不能只归档。`DELETE /api/conv/<id>` 只是置 `archived=1`，
   会话仍然占着列表位置，跑几轮就堆一片。
"""
import os
import sqlite3
import time

DB = os.path.expanduser("~/.data/agentd/agentd.db")


def new_conv(title="ui-test"):
    """建一个空会话，返回 cid 字符串（和 URL 参数同类型）"""
    c = sqlite3.connect(DB)
    now = time.strftime("%Y-%m-%dT%H:%M:%S")
    cur = c.execute(
        "INSERT INTO conversations(title,created_at,updated_at) VALUES(?,?,?)",
        (title, now, now),
    )
    c.commit()
    cid = str(cur.lastrowid)
    c.close()
    return cid


def drop_conv(*cids):
    """连消息一起删掉，忽略空 id"""
    ids = [int(x) for x in cids if x not in (None, "", "null")]
    if not ids:
        return
    c = sqlite3.connect(DB)
    for cid in ids:
        c.execute("delete from messages where conv_id=?", (cid,))
        c.execute("delete from conversations where id=?", (cid,))
    c.commit()
    c.close()
