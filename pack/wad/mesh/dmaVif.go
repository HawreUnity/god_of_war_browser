package mesh

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/mogaika/god_of_war_browser/ps2/dma"
	"github.com/mogaika/god_of_war_browser/ps2/vif"
)

var unpackBuffersBases = []uint32{0, 0x155, 0x2ab}

const GSFixedPoint8 = 16.0
const GSFixedPoint24 = 4096.0

type MeshParserStream struct {
	Data                []byte
	Offset              uint32
	Packets             []Packet
	Object              *Object
	Log                 *Logger
	state               *MeshParserState
	lastPacketDataStart uint32
	lastPacketDataEnd   uint32
}

type MeshParserState struct {
	XYZW       []byte
	RGBA       []byte
	UV         []byte
	UVWidth    int
	Norm       []byte
	Boundaries []byte
	VertexMeta []byte
	Buffer     int
}

func NewMeshParserStream(allb []byte, object *Object, packetOffset uint32, exlog *Logger) *MeshParserStream {
	return &MeshParserStream{
		Data:    allb,
		Object:  object,
		Offset:  packetOffset,
		Log:     exlog,
		Packets: make([]Packet, 0),
	}
}

func (ms *MeshParserStream) flushState() error {
	if ms.state != nil {
		packet, err := ms.state.ToPacket(ms.Log, ms.lastPacketDataStart)
		if err != nil {
			return err
		}
		if packet != nil {
			ms.Packets = append(ms.Packets, *packet)
		}
	}
	ms.state = &MeshParserState{}
	return nil
}

func (ms *MeshParserStream) ParsePackets() error {
	for i := uint32(0); i < ms.Object.PacketsPerFilter; i++ {
		dmaPackPos := ms.Offset + i*0x10
		dmaPack := dma.NewTag(binary.LittleEndian.Uint64(ms.Data[dmaPackPos:]))

		ms.Log.Printf("           -  dma offset: 0x%.8x packet: %d pos: 0x%.6x rows: 0x%.4x end: 0x%.6x", dmaPackPos,
			i, dmaPack.Addr()+ms.Object.Offset, dmaPack.QWC(), dmaPack.Addr()+ms.Object.Offset+uint32(dmaPack.QWC()*16))
		ms.Log.Printf("          | %v", dmaPack)
		switch dmaPack.ID() {
		case dma.DMA_TAG_REF:
			ms.lastPacketDataStart = dmaPack.Addr() + ms.Object.Offset
			ms.lastPacketDataEnd = ms.lastPacketDataStart + dmaPack.QWC()*0x10
			ms.Log.Printf("            -vif pack start: 0x%.6x + 0x%.6x = 0x%.6x => 0x%.6x", ms.Offset, dmaPack.Addr(), ms.lastPacketDataStart, ms.lastPacketDataEnd)
			if err := ms.ParseVif(); err != nil {
				return fmt.Errorf("Error when parsing vif stream triggered by dma_tag_ref: %v", err)
			}
		case dma.DMA_TAG_RET:
			if dmaPack.QWC() != 0 {
				return fmt.Errorf("Not support dma_tag_ret with qwc != 0 (%d)", dmaPack.QWC())
			}
			if i != ms.Object.PacketsPerFilter-1 {
				return fmt.Errorf("dma_tag_ret not in end of stream (%d != %d)", i, ms.Object.PacketsPerFilter-1)
			} else {
				ms.Log.Printf("             << dma_tag_ret at 0x%.8x >>", dmaPackPos)
			}
		default:
			return fmt.Errorf("Unknown dma packet %v in mesh stream at 0x%.8x i = 0x%.2x < 0x%.2x", dmaPack, dmaPackPos, i, ms.Object.PacketsPerFilter)
		}
	}
	ms.flushState()
	return nil
}

func (state *MeshParserState) ToPacket(exlog *Logger, debugPos uint32) (*Packet, error) {
	if state.XYZW == nil {
		if state.UV != nil || state.Norm != nil || state.VertexMeta != nil || state.RGBA != nil {
			return nil, fmt.Errorf("Empty xyzw array, possibly incorrect data: 0x%x. State: %+#v", debugPos, state)
		}
		return nil, nil
	}

	packet := &Packet{HasTransparentBlending: false}
	packet.Offset = debugPos

	countTrias := len(state.XYZW) / 8
	packet.Trias.X = make([]float32, countTrias)
	packet.Trias.Y = make([]float32, countTrias)
	packet.Trias.Z = make([]float32, countTrias)
	packet.Trias.Skip = make([]bool, countTrias)
	for i := range packet.Trias.X {
		bp := i * 8
		packet.Trias.X[i] = float32(int16(binary.LittleEndian.Uint16(state.XYZW[bp:bp+2]))) / GSFixedPoint8
		packet.Trias.Y[i] = float32(int16(binary.LittleEndian.Uint16(state.XYZW[bp+2:bp+4]))) / GSFixedPoint8
		packet.Trias.Z[i] = float32(int16(binary.LittleEndian.Uint16(state.XYZW[bp+4:bp+6]))) / GSFixedPoint8
		packet.Trias.Skip[i] = state.XYZW[bp+7]&0x80 != 0
		// exlog.Printf(" %.5d == %f %f %f %t", i, packet.Trias.X[i], packet.Trias.Y[i], packet.Trias.Z[i], packet.Trias.Skip[i])
	}

	if state.UV != nil {
		switch state.UVWidth {
		case 2:
			uvCount := len(state.UV) / 4
			packet.Uvs.U = make([]float32, uvCount)
			packet.Uvs.V = make([]float32, uvCount)
			for i := range packet.Uvs.U {
				bp := i * 4
				packet.Uvs.U[i] = float32(int16(binary.LittleEndian.Uint16(state.UV[bp:bp+2]))) / GSFixedPoint24
				packet.Uvs.V[i] = float32(int16(binary.LittleEndian.Uint16(state.UV[bp+2:bp+4]))) / GSFixedPoint24
			}
		case 4:
			uvCount := len(state.UV) / 8
			packet.Uvs.U = make([]float32, uvCount)
			packet.Uvs.V = make([]float32, uvCount)
			for i := range packet.Uvs.U {
				bp := i * 8
				packet.Uvs.U[i] = float32(int32(binary.LittleEndian.Uint32(state.UV[bp:bp+4]))) / GSFixedPoint24
				packet.Uvs.V[i] = float32(int32(binary.LittleEndian.Uint32(state.UV[bp+4:bp+8]))) / GSFixedPoint24
			}
		}
	}

	if state.Norm != nil {
		normcnt := len(state.Norm) / 3
		packet.Norms.X = make([]float32, normcnt)
		packet.Norms.Y = make([]float32, normcnt)
		packet.Norms.Z = make([]float32, normcnt)
		for i := range packet.Norms.X {
			bp := i * 3
			packet.Norms.X[i] = float32(int8(state.Norm[bp])) / 100.0
			packet.Norms.Y[i] = float32(int8(state.Norm[bp+1])) / 100.0
			packet.Norms.Z[i] = float32(int8(state.Norm[bp+2])) / 100.0
		}
	}

	if state.RGBA != nil {
		rgbacnt := len(state.RGBA) / 4
		packet.Blend.R = make([]uint16, rgbacnt)
		packet.Blend.G = make([]uint16, rgbacnt)
		packet.Blend.B = make([]uint16, rgbacnt)
		packet.Blend.A = make([]uint16, rgbacnt)
		for i := range packet.Blend.R {
			bp := i * 4
			packet.Blend.R[i] = uint16(state.RGBA[bp])
			packet.Blend.G[i] = uint16(state.RGBA[bp+1])
			packet.Blend.B[i] = uint16(state.RGBA[bp+2])
			packet.Blend.A[i] = uint16(state.RGBA[bp+3])
		}
		for _, a := range packet.Blend.A {
			if a < 0x80 {
				packet.HasTransparentBlending = true
				break
			}
		}
	}

	if state.Boundaries != nil {
		for i := range packet.Boundaries {
			packet.Boundaries[i] = math.Float32frombits(binary.LittleEndian.Uint32(state.Boundaries[i*4 : i*4+4]))
		}
	}

	if state.VertexMeta != nil {
		blocks := len(state.VertexMeta) / 0x10
		packet.VertexMeta = state.VertexMeta
		vertexes := len(packet.Trias.X)

		packet.Joints = make([]uint16, vertexes)

		vertnum := 0
		for i := 0; i < blocks; i++ {
			block := state.VertexMeta[i*16 : i*16+16]
			if i == 0 && block[4] == 4 {
				packet.Joints2 = make([]uint16, vertexes)
			}

			block_verts := int(block[0])

			for j := 0; j < block_verts; j++ {
				packet.Joints[vertnum+j] = uint16(block[13] >> 4)
				if packet.Joints2 != nil {
					packet.Joints2[vertnum+j] = uint16(block[12] >> 2)
				}
			}

			vertnum += block_verts

			if block[1]&0x80 != 0 {
				if i != blocks-1 {
					return nil, fmt.Errorf("Block count != blocks: %v <= %v", blocks, i)
				}
			}
		}
		if vertnum != vertexes {
			return nil, fmt.Errorf("Vertnum != vertexes count: %v <= %v", vertnum, vertexes)
		}
	}

	exlog.Printf("    = Flush xyzw:%t, rgba:%t, uv:%t, norm:%t, vmeta:%t (%d)",
		state.XYZW != nil, state.RGBA != nil, state.UV != nil,
		state.Norm != nil, state.VertexMeta != nil, len(packet.Trias.X))

	atoStr := func(a []byte) string {
		u16 := func(barr []byte, id int) uint16 {
			return binary.LittleEndian.Uint16(barr[id*2 : id*2+2])
		}
		u32 := func(barr []byte, id int) uint32 {
			return binary.LittleEndian.Uint32(barr[id*4 : id*4+4])
		}
		f32 := func(barr []byte, id int) float32 {
			return math.Float32frombits(u32(barr, id))
		}
		return fmt.Sprintf(" %.4x %.4x  %.4x %.4x   %.4x %.4x  %.4x %.4x   |  %.8x %.8x %.8x %.8x |  %f  %f  %f  %f",
			u16(a, 0), u16(a, 1), u16(a, 2), u16(a, 3),
			u16(a, 4), u16(a, 5), u16(a, 6), u16(a, 7),
			u32(a, 0), u32(a, 1), u32(a, 2), u32(a, 3),
			f32(a, 0), f32(a, 1), f32(a, 2), f32(a, 3),
		)
	}

	if state.VertexMeta != nil {
		exlog.Printf("         Vertex Meta:")
		for i := 0; i < len(packet.VertexMeta)/16; i++ {
			exlog.Printf("  %s", atoStr(packet.VertexMeta[i*16:i*16]))
		}
	}

	if state.Boundaries != nil {
		exlog.Printf("         Boundaries: %v", packet.Boundaries)
	}

	return packet, nil
}

func (ms *MeshParserStream) ParseVif() error {
	data := ms.Data[ms.lastPacketDataStart:ms.lastPacketDataEnd]
	pos := uint32(0)
	for {
		pos = ((pos + 3) / 4) * 4
		if pos >= uint32(len(data)) {
			break
		}
		tagPos := pos
		rawVifCode := binary.LittleEndian.Uint32(data[pos:])
		//if rawVifCode == 0xffffffff {
		//ms.Log.Printf("rawVifCode == %.8x, aborting %v", rawVifCode, data[pos:])
		//}
		vifCode := vif.NewCode(rawVifCode)

		pos += 4
		if vifCode.Cmd() > 0x60 {
			vifComponents := ((vifCode.Cmd() >> 2) & 0x3) + 1
			vifWidth := []uint32{32, 16, 8, 4}[vifCode.Cmd()&0x3]

			vifBlockSize := uint32(vifComponents) * ((vifWidth * uint32(vifCode.Num())) / 8)

			vifIsSigned := (vifCode.Imm()>>14)&1 == 0
			vifUseTops := (vifCode.Imm()>>15)&1 != 0
			vifTarget := uint32(vifCode.Imm() & 0x3ff)

			vifBufferBase := 1
			for _, base := range unpackBuffersBases {
				if vifTarget >= base {
					vifBufferBase++
				} else {
					break
				}
			}
			if ms.state == nil {
				ms.state = &MeshParserState{Buffer: vifBufferBase}
			} else if vifBufferBase != ms.state.Buffer {
				if err := ms.flushState(); err != nil {
					return err
				}
				ms.state.Buffer = vifBufferBase
			}
			handledBy := ""

			defer func() {
				if r := recover(); r != nil {
					ms.Log.Printf("[%.4s] !! !! panic on unpack: 0x%.2x elements: 0x%.2x components: %d width: %.2d target: 0x%.3x sign: %t tops: %t size: %.6x",
						handledBy, vifCode.Cmd(), vifCode.Num(), vifComponents, vifWidth, vifTarget, vifIsSigned, vifUseTops, vifBlockSize)
					panic(r)
				}
			}()

			vifBlock := data[pos : pos+vifBlockSize]

			errorAlreadyPresent := func(handler string) error {
				ms.Log.Printf("[%.4s]++> unpack: 0x%.2x elements: 0x%.2x components: %d width: %.2d target: 0x%.3x sign: %t tops: %t size: %.6x",
					handledBy, vifCode.Cmd(), vifCode.Num(), vifComponents, vifWidth, vifTarget, vifIsSigned, vifUseTops, vifBlockSize)
				return fmt.Errorf("%s already present. What is this: %.6x ?", handler, tagPos+ms.lastPacketDataStart)
			}

			switch vifWidth {
			case 32:
				if vifIsSigned {
					switch vifComponents {
					case 4: // joints and format info all time after data (i think)
						switch vifTarget {
						case 0x000, 0x155, 0x2ab:
							if ms.state.VertexMeta != nil {
								return errorAlreadyPresent("Vertex Meta")
							}

							ms.state.VertexMeta = vifBlock
							handledBy = "vmta"
						default:
							if ms.state.Boundaries != nil {
								return errorAlreadyPresent("Boundaries")
							}
							ms.state.Boundaries = vifBlock
							handledBy = "bndr"
						}
					case 2:
						handledBy = " uv4"
						if ms.state.UV == nil {
							ms.state.UV = vifBlock
							handledBy = " uv2"
							ms.state.UVWidth = 4
						} else {
							return errorAlreadyPresent("UV")
						}
					}
				}
			case 16:
				if vifIsSigned {
					switch vifComponents {
					case 4:
						if ms.state.XYZW == nil {
							ms.state.XYZW = vifBlock
							handledBy = "xyzw"
						} else {
							return errorAlreadyPresent("XYZW")
						}
					case 2:
						if ms.state.UV == nil {
							ms.state.UV = vifBlock
							handledBy = " uv2"
							ms.state.UVWidth = 2
						} else {
							return errorAlreadyPresent("UV")
						}
					}
				}
			case 8:
				if vifIsSigned {
					switch vifComponents {
					case 3:
						if ms.state.Norm == nil {
							ms.state.Norm = vifBlock
							handledBy = "norm"
						} else {
							return errorAlreadyPresent("Norm")
						}
					}
				} else {
					switch vifComponents {
					case 4:
						if ms.state.RGBA == nil {
							ms.state.RGBA = vifBlock
							handledBy = "rgba"
						} else {
							return errorAlreadyPresent("RGBA")
						}
					}
				}
			}

			ms.Log.Printf("[%.4s] + unpack: 0x%.2x cmd: 0x%.2x elements: 0x%.2x components: %d width: %.2d target: 0x%.3x sign: %t tops: %t size: %.6x",
				handledBy, ms.lastPacketDataStart+tagPos, vifCode.Cmd(), vifCode.Num(), vifComponents, vifWidth, vifTarget, vifIsSigned, vifUseTops, vifBlockSize)
			if handledBy == "" {
				return fmt.Errorf("Block 0x%.6x (cmd 0x%.2x; %d bit; %d components; %d elements; sign %t; tops %t; target: %.3x; size: %.6x) not handled",
					tagPos+ms.lastPacketDataStart, vifCode.Cmd(), vifWidth, vifComponents, vifCode.Num(), vifIsSigned, vifUseTops, vifTarget, vifBlockSize)
			}
			pos += vifBlockSize
		} else {
			ms.Log.Printf("# vif %v", vifCode)
			switch vifCode.Cmd() {
			case vif.VIF_CMD_MSCAL:
				if err := ms.flushState(); err != nil {
					return err
				}
			case vif.VIF_CMD_STROW:
				pos += 0x10
			default:
				ms.Log.Printf("     - unknown VIF: %v", vifCode)
			}
		}
	}

	return nil
}
