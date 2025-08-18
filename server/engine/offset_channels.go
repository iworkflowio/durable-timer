package engine

import "fmt"


func NewOffsetChannelPerShard() *OffsetChannelPerShard {
	return &OffsetChannelPerShard{
		channels: make(map[int]chan *TimerOffset),
	}
}

func (c *OffsetChannelPerShard) GetSendChannel(shardId int) chan <-*TimerOffset {
	c.Lock()
	defer c.Unlock()

	ch, ok := c.channels[shardId]
	if !ok {
		panic(fmt.Sprintf("shardId %d not found", shardId))
	}

	return ch
}

func (c *OffsetChannelPerShard) GetReceiveChannel(shardId int) <-chan *TimerOffset {
	c.Lock()
	defer c.Unlock()

	ch, ok := c.channels[shardId]
	if !ok {
		panic(fmt.Sprintf("shardId %d not found", shardId))
	}

	return ch
}

func (c *OffsetChannelPerShard) AddChannel(shardId int) {
	c.Lock()
	defer c.Unlock()

	_, ok := c.channels[shardId]
	if ok {
		panic(fmt.Sprintf("shardId %d already exists", shardId))
	}

	c.channels[shardId] = make(chan *TimerOffset)
}

func (c *OffsetChannelPerShard) RemoveChannel(shardId int) {
	c.Lock()
	defer c.Unlock()

	ch, ok := c.channels[shardId]
	if !ok {
		panic(fmt.Sprintf("shardId %d not found", shardId))
	}

	close(ch)
	delete(c.channels, shardId)
}