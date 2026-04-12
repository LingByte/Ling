package utils

import (
	"errors"
	"os"
	"strconv"
	"sync"
	"time"
)

var SnowflakeUtil *Snowflake

func init() {
	s, err := NewSnowflake()
	if err != nil {
		panic(err)
	}
	SnowflakeUtil = s
}

const (
	timestampBits = 41
	machineIDBits = 10
	sequenceBits  = 12

	maxMachineID = 1<<machineIDBits - 1
	maxSequence  = 1<<sequenceBits - 1

	timestampShift = machineIDBits + sequenceBits
	machineIDShift = sequenceBits

	epoch int64 = 1609459200000 // 2021-01-01 毫秒级
)

type Snowflake struct {
	mu        sync.Mutex
	lastStamp int64
	sequence  int64
	machineID int64
}

func NewSnowflake() (*Snowflake, error) {
	mid := getMachineID()
	if mid < 0 || mid > maxMachineID {
		return nil, errors.New("machine id out of range")
	}
	return &Snowflake{
		machineID: mid,
		sequence:  0,
		lastStamp: time.Now().UnixMilli(),
	}, nil
}

// NextID 生成 唯一 正数 int64
func (sf *Snowflake) NextID() int64 {
	sf.mu.Lock()
	defer sf.mu.Unlock()

	now := time.Now().UnixMilli()

	if now < sf.lastStamp {
		for now < sf.lastStamp {
			now = time.Now().UnixMilli()
		}
	}

	if now == sf.lastStamp {
		sf.sequence = (sf.sequence + 1) & maxSequence
		if sf.sequence == 0 {
			for now <= sf.lastStamp {
				now = time.Now().UnixMilli()
			}
		}
	} else {
		sf.sequence = 0
	}

	sf.lastStamp = now

	// 标准格式：0 | 41位时间 | 10位机器 | 12位序列
	return ((now - epoch) << timestampShift) |
		(sf.machineID << machineIDShift) |
		sf.sequence
}

// GenID 字符串格式（正数）
func (sf *Snowflake) GenID() string {
	return strconv.FormatInt(sf.NextID(), 10)
}

func getMachineID() int64 {
	midStr := os.Getenv("MACHINE_ID")
	if midStr == "" {
		return 1
	}
	mid, err := strconv.ParseInt(midStr, 10, 64)
	if err != nil {
		return 1
	}
	return mid
}
