package common

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/abema/go-mp4"
	"github.com/go-audio/aiff"
	"github.com/go-audio/wav"
	"github.com/jfreymuth/oggvorbis"
	"github.com/mewkiz/flac"
	"github.com/pkg/errors"
	"github.com/tcolgate/mp3"
	"github.com/yapingcat/gomedia/go-codec"
)

// GetAudioDuration 使用纯 Go 库获取音频文件的时长（秒）。
// 它不再依赖外部的 ffmpeg 或 ffprobe 程序。
func GetAudioDuration(ctx context.Context, f io.ReadSeeker, ext string) (duration float64, err error) {
	SysLog(LogText("GetAudioDuration: ext=%s", ext))
	ext = strings.ToLower(ext)
	// 根据文件扩展名选择解析器
	switch ext {
	case ".mp3":
		duration, err = getMP3Duration(f)
	case ".wav":
		duration, err = getWAVDuration(f)
	case ".flac":
		duration, err = getFLACDuration(f)
	case ".m4a", ".mp4":
		duration, err = getM4ADuration(f)
	case ".ogg", ".oga", ".opus":
		duration, err = getOGGDuration(f)
		if err != nil {
			duration, err = getOpusDuration(f)
		}
	case ".aiff", ".aif", ".aifc":
		duration, err = getAIFFDuration(f)
	case ".webm":
		duration, err = getWebMDuration(f)
	case ".aac":
		duration, err = getAACDuration(f)
	default:
		return 0, fmt.Errorf("unsupported audio format: %s", ext)
	}
	if err == nil && (math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0) {
		return 0, fmt.Errorf("invalid audio duration: %v", duration)
	}
	SysLog(LogText("GetAudioDuration: duration=%f", duration))
	return duration, err
}

// getMP3Duration 解析 MP3 文件以获取时长。
// 注意：对于 VBR (Variable Bitrate) MP3，这个估算可能不完全精确，但通常足够好。
// FFmpeg 在这种情况下会扫描整个文件来获得精确值，但这里的库提供了快速估算。
func getMP3Duration(r io.Reader) (float64, error) {
	d := mp3.NewDecoder(r)
	var f mp3.Frame
	skipped := 0
	duration := 0.0

	for {
		if err := d.Decode(&f, &skipped); err != nil {
			if err == io.EOF {
				break
			}
			return 0, errors.Wrap(err, "failed to decode mp3 frame")
		}
		duration += f.Duration().Seconds()
	}
	if duration <= 0 {
		return 0, errors.New("no valid mp3 frames found")
	}
	return duration, nil
}

// hasValidTrailingRIFFChunks reports whether [startPos, endPos) consists solely
// of well-formed RIFF subchunks (e.g. LIST/INFO/id3). When a WAV file spoofs a
// tiny data chunk size in front of real PCM audio, the remaining bytes will not
// parse as valid RIFF chunks summing to endPos.
func hasValidTrailingRIFFChunks(r io.ReadSeeker, dataStart, pcmSize, endPos int64) bool {
	pos := dataStart + pcmSize
	if pcmSize%2 == 1 && pos < endPos {
		pos++
	}
	if pos >= endPos {
		return true
	}
	var hdr [8]byte
	for pos < endPos {
		if endPos-pos < 8 {
			return false
		}
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return false
		}
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return false
		}
		for i := 0; i < 4; i++ {
			if hdr[i] < 0x20 || hdr[i] > 0x7e {
				return false
			}
		}
		chunkSize := int64(binary.LittleEndian.Uint32(hdr[4:8]))
		next := pos + 8 + chunkSize
		if next > endPos {
			return false
		}
		if chunkSize%2 == 1 && next < endPos {
			next++
		}
		pos = next
	}
	return pos == endPos
}

// getWAVDuration 解析 WAV 文件头以获取时长。
func getWAVDuration(r io.ReadSeeker) (float64, error) {
	// 1. 强制复位指针
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, errors.Wrap(err, "failed to seek wav file")
	}

	dec := wav.NewDecoder(r)

	// IsValidFile 会读取 fmt 块
	if !dec.IsValidFile() {
		return 0, errors.New("invalid wav file")
	}

	// 尝试寻找 data 块
	if err := dec.FwdToPCM(); err != nil {
		return 0, errors.Wrap(err, "failed to find PCM data chunk")
	}

	pcmSize := int64(dec.PCMSize)
	currentPos, _ := r.Seek(0, io.SeekCurrent)
	endPos, _ := r.Seek(0, io.SeekEnd)
	_, _ = r.Seek(currentPos, io.SeekStart)
	actualPCM := endPos - currentPos

	// 如果读出来的 Size <= 0，或后续字节并非合法 RIFF 子块（伪造极小 data chunk size 规避计费），
	// 使用实际剩余音频数据长度计算时长。
	if pcmSize <= 0 || (actualPCM > pcmSize && !hasValidTrailingRIFFChunks(r, currentPos, pcmSize, endPos)) {
		if actualPCM > 0 {
			pcmSize = actualPCM
		} else if endPos > 44 {
			pcmSize = endPos - 44
		}
	}

	numChans := int64(dec.NumChans)
	bitDepth := int64(dec.BitDepth)
	sampleRate := float64(dec.SampleRate)

	if sampleRate <= 0 || numChans <= 0 || bitDepth < 8 || pcmSize <= 0 {
		return 0, errors.New("invalid wav header metadata")
	}

	bytesPerFrame := numChans * (bitDepth / 8)
	if bytesPerFrame <= 0 {
		return 0, errors.New("invalid byte depth calculation")
	}

	durationSeconds := float64(pcmSize) / (float64(bytesPerFrame) * sampleRate)
	if math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) || durationSeconds <= 0 {
		return 0, errors.New("invalid wav duration")
	}
	return durationSeconds, nil
}

// getFLACDuration 解析 FLAC 文件的 STREAMINFO 与音频帧，防止 NSamples=0 或伪造极小采样数规避计费。
func getFLACDuration(r io.ReadSeeker) (float64, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, errors.Wrap(err, "failed to seek flac file")
	}
	stream, err := flac.Parse(r)
	if err != nil {
		return 0, errors.Wrap(err, "failed to parse flac stream")
	}
	defer stream.Close()

	if stream.Info == nil || stream.Info.SampleRate == 0 {
		return 0, errors.New("invalid flac stream info")
	}

	var countedSamples uint64
	for {
		f, ferr := stream.ParseNext()
		if ferr == io.EOF {
			break
		}
		if ferr != nil {
			if countedSamples == 0 && stream.Info.NSamples == 0 {
				return 0, errors.Wrap(ferr, "failed to decode flac frames")
			}
			break
		}
		countedSamples += uint64(f.BlockSize)
	}
	totalSamples := stream.Info.NSamples
	if countedSamples > totalSamples {
		totalSamples = countedSamples
	}
	if totalSamples == 0 {
		return 0, errors.New("flac stream contains no audio samples")
	}

	duration := float64(totalSamples) / float64(stream.Info.SampleRate)
	return duration, nil
}

// getM4ADuration 解析 M4A/MP4 文件的 'mvhd'、轨道 'mdhd' 与样本表以防伪造零时长。
func getM4ADuration(r io.ReadSeeker) (float64, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, errors.Wrap(err, "failed to seek m4a/mp4 file")
	}
	info, err := mp4.Probe(r)
	if err != nil {
		return 0, errors.Wrap(err, "failed to probe m4a/mp4 file")
	}
	var bestDuration float64
	if info.Timescale > 0 && info.Duration > 0 {
		bestDuration = float64(info.Duration) / float64(info.Timescale)
	}
	for _, track := range info.Tracks {
		if track == nil || track.Timescale == 0 {
			continue
		}
		if track.Duration > 0 {
			if td := float64(track.Duration) / float64(track.Timescale); td > bestDuration {
				bestDuration = td
			}
		}
		if len(track.Samples) > 0 {
			var sampleTicks uint64
			for _, s := range track.Samples {
				sampleTicks += uint64(s.TimeDelta)
			}
			if sampleTicks > 0 {
				if sd := float64(sampleTicks) / float64(track.Timescale); sd > bestDuration {
					bestDuration = sd
				}
			}
		}
	}
	if math.IsNaN(bestDuration) || math.IsInf(bestDuration, 0) || bestDuration <= 0 {
		return 0, errors.New("invalid m4a/mp4 duration")
	}
	return bestDuration, nil
}

// getOGGDuration 解析 OGG/Vorbis 文件以获取时长。
func getOGGDuration(r io.ReadSeeker) (float64, error) {
	// 重置 reader 到开头
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, errors.Wrap(err, "failed to seek ogg file")
	}

	reader, err := oggvorbis.NewReader(r)
	if err != nil {
		return 0, errors.Wrap(err, "failed to create ogg vorbis reader")
	}

	// 计算时长 = 总采样数 / 采样率
	// 需要读取整个文件来获取总采样数
	channels := reader.Channels()
	sampleRate := reader.SampleRate()
	if channels <= 0 || sampleRate <= 0 {
		return 0, errors.New("invalid ogg vorbis metadata")
	}

	// 估算方法：读取到文件结尾
	var totalSamples int64
	buf := make([]float32, 4096*channels)
	for {
		n, err := reader.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, errors.Wrap(err, "failed to read ogg samples")
		}
		totalSamples += int64(n / channels)
	}
	if totalSamples <= 0 {
		return 0, errors.New("ogg vorbis stream contains no audio samples")
	}

	duration := float64(totalSamples) / float64(sampleRate)
	return duration, nil
}

// opusFrameSamplesByConfig maps RFC 6716 Section 3.1 TOC config (0..31) to
// samples per frame at 48000 Hz.
var opusFrameSamplesByConfig = [32]int64{
	480, 960, 1920, 2880, // 0..3: SILK NB 10/20/40/60ms
	480, 960, 1920, 2880, // 4..7: SILK MB
	480, 960, 1920, 2880, // 8..11: SILK WB
	480, 960, // 12..13: Hybrid SWB 10/20ms
	480, 960, // 14..15: Hybrid FB 10/20ms
	120, 240, 480, 960, // 16..19: CELT NB 2.5/5/10/20ms
	120, 240, 480, 960, // 20..23: CELT WB
	120, 240, 480, 960, // 24..27: CELT SWB
	120, 240, 480, 960, // 28..31: CELT FB
}

func opusPacketSamples(pktHeader []byte) int64 {
	if len(pktHeader) == 0 {
		return 0
	}
	if len(pktHeader) >= 8 {
		tag := string(pktHeader[:8])
		if tag == "OpusHead" || tag == "OpusTags" {
			return 0
		}
	}
	toc := pktHeader[0]
	frameSamples := opusFrameSamplesByConfig[toc>>3]
	var frameCount int64
	switch toc & 0x03 {
	case 0:
		frameCount = 1
	case 1, 2:
		frameCount = 2
	case 3:
		if len(pktHeader) < 2 {
			return 0
		}
		frameCount = int64(pktHeader[1] & 0x3f)
		if frameCount == 0 {
			return 0
		}
	}
	return frameSamples * frameCount
}

// getOpusDuration 解析 Opus 文件（在 OGG 容器中）以获取时长。
// R14-010: 同时按逻辑流（BOS/serial）累加链式 Ogg granule position，并按 RFC 6716 TOC
// 字节统计实际音频帧时长，取较大值以防止链式 Ogg 或负 granule 伪造规避 STT 计费。
func getOpusDuration(r io.ReadSeeker) (float64, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, errors.Wrap(err, "failed to seek opus file")
	}

	type oggStreamState struct {
		maxGranule int64
		inPacket   bool
		hdr        [8]byte
		hdrLen     int
	}
	activeStreams := make(map[uint32]*oggStreamState)
	var finishedGranuleSamples int64
	var totalPacketSamples int64
	foundPage := false
	buf := make([]byte, 27) // OGG 页面头部最小大小

	for {
		n, err := r.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, errors.Wrap(err, "failed to read opus/ogg page")
		}
		if n < 27 {
			break
		}

		// 检查 OGG 页面标识 "OggS"
		if string(buf[0:4]) != "OggS" {
			if _, err := r.Seek(-26, io.SeekCurrent); err != nil {
				break
			}
			continue
		}
		foundPage = true

		headerType := buf[5]
		granulePos := int64(binary.LittleEndian.Uint64(buf[6:14]))
		serial := binary.LittleEndian.Uint32(buf[14:18])

		stream := activeStreams[serial]
		if headerType&0x02 != 0 {
			// New chained logical bitstream (BOS): bank any previous stream's granule first.
			if stream != nil && stream.maxGranule > 0 {
				finishedGranuleSamples += stream.maxGranule
			}
			stream = &oggStreamState{}
			activeStreams[serial] = stream
		} else if stream == nil {
			stream = &oggStreamState{}
			activeStreams[serial] = stream
		}
		if granulePos > stream.maxGranule {
			stream.maxGranule = granulePos
		}
		if headerType&0x01 == 0 {
			stream.inPacket = false
			stream.hdrLen = 0
		}

		numSegments := int(buf[26])
		segmentTable := make([]byte, numSegments)
		if _, err := io.ReadFull(r, segmentTable); err != nil {
			break
		}

		var pageSize int
		for _, segSize := range segmentTable {
			pageSize += int(segSize)
		}
		pageData := make([]byte, pageSize)
		if _, err := io.ReadFull(r, pageData); err != nil {
			break
		}

		offset := 0
		for _, segSize := range segmentTable {
			s := int(segSize)
			if !stream.inPacket {
				stream.inPacket = true
				stream.hdrLen = 0
			}
			if stream.hdrLen < len(stream.hdr) && s > 0 {
				need := len(stream.hdr) - stream.hdrLen
				if need > s {
					need = s
				}
				copy(stream.hdr[stream.hdrLen:], pageData[offset:offset+need])
				stream.hdrLen += need
			}
			offset += s
			if s < 255 {
				totalPacketSamples += opusPacketSamples(stream.hdr[:stream.hdrLen])
				stream.inPacket = false
				stream.hdrLen = 0
			}
		}

		if headerType&0x04 != 0 {
			// EOS: bank this logical bitstream immediately.
			if stream.maxGranule > 0 {
				finishedGranuleSamples += stream.maxGranule
			}
			delete(activeStreams, serial)
		}
	}

	totalGranuleSamples := finishedGranuleSamples
	for _, stream := range activeStreams {
		if stream.maxGranule > 0 {
			totalGranuleSamples += stream.maxGranule
		}
	}
	effectiveSamples := totalGranuleSamples
	if totalPacketSamples > effectiveSamples {
		effectiveSamples = totalPacketSamples
	}
	if !foundPage || effectiveSamples <= 0 {
		return 0, errors.New("invalid opus/ogg stream: missing valid granule position")
	}

	// Opus 的采样率固定为 48000 Hz
	duration := float64(effectiveSamples) / 48000.0
	return duration, nil
}

// parseAIFFExtended80 decodes an 80-bit IEEE 754 extended-precision float (big-endian)
// used for sampleRate in AIFF COMM chunks.
func parseAIFFExtended80(b []byte) float64 {
	if len(b) < 10 {
		return 0
	}
	exp := int(binary.BigEndian.Uint16(b[0:2])&0x7fff) - 16383
	mantissa := binary.BigEndian.Uint64(b[2:10])
	if mantissa == 0 || exp < -63 || exp > 63 {
		return 0
	}
	val := math.Ldexp(float64(mantissa), exp-63)
	if b[0]&0x80 != 0 {
		val = -val
	}
	return val
}

// scanAIFFPCMDuration scans AIFF/AIFC COMM and SSND chunks directly to compute the
// actual PCM audio duration lower bound, preventing forged tiny COMM.numSampleFrames (R14-010).
func scanAIFFPCMDuration(r io.ReadSeeker) float64 {
	fileSize, err := r.Seek(0, io.SeekEnd)
	if err != nil || fileSize < 12 {
		return 0
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0
	}
	var formHdr [12]byte
	if _, err := io.ReadFull(r, formHdr[:]); err != nil {
		return 0
	}
	if string(formHdr[0:4]) != "FORM" {
		return 0
	}
	formType := string(formHdr[8:12])
	if formType != "AIFF" && formType != "AIFC" {
		return 0
	}

	var numChans int64
	var bitDepth int64
	var sampleRate float64
	var compressionType string
	var ssndPCMBytes int64
	pos := int64(12)

	for pos+8 <= fileSize {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			break
		}
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			break
		}
		chunkID := string(hdr[0:4])
		chunkSize := int64(binary.BigEndian.Uint32(hdr[4:8]))
		dataStart := pos + 8
		avail := fileSize - dataStart
		if avail < 0 {
			break
		}
		switch chunkID {
		case "COMM":
			readLen := chunkSize
			if readLen > avail {
				readLen = avail
			}
			if readLen >= 18 {
				bufLen := readLen
				if bufLen > 22 {
					bufLen = 22
				}
				buf := make([]byte, bufLen)
				if _, err := io.ReadFull(r, buf); err == nil {
					numChans = int64(binary.BigEndian.Uint16(buf[0:2]))
					bitDepth = int64(binary.BigEndian.Uint16(buf[6:8]))
					sampleRate = parseAIFFExtended80(buf[8:18])
					if formType == "AIFC" && len(buf) >= 22 {
						compressionType = string(buf[18:22])
					}
				}
			}
		case "SSND":
			var ssndHdr [8]byte
			ssndOffset := int64(0)
			if avail >= 8 {
				if _, err := io.ReadFull(r, ssndHdr[:]); err == nil {
					ssndOffset = int64(binary.BigEndian.Uint32(ssndHdr[0:4]))
				}
			}
			pcmStart := dataStart + 8 + ssndOffset
			if pcmStart <= fileSize {
				remaining := fileSize - pcmStart
				declared := chunkSize - 8 - ssndOffset
				if declared <= 0 {
					// If SSND chunkSize was forged to 0/tiny on the trailing chunk, use actual bytes.
					if chunkSize == 0 || dataStart+chunkSize >= fileSize {
						declared = remaining
					}
				}
				if declared > remaining {
					declared = remaining
				}
				if declared > ssndPCMBytes {
					ssndPCMBytes = declared
				}
			}
		}
		padded := chunkSize
		if padded%2 != 0 {
			padded++
		}
		if padded <= 0 && chunkID == "SSND" {
			break
		}
		nextPos := dataStart + padded
		if nextPos <= pos {
			break
		}
		pos = nextPos
	}

	if numChans <= 0 || sampleRate <= 0 || ssndPCMBytes <= 0 {
		return 0
	}
	bytesPerSample := (bitDepth + 7) / 8
	if formType == "AIFC" {
		switch compressionType {
		case "", "NONE", "sowt", "twos", "raw ":
			// uncompressed PCM
		case "ulaw", "ULAW", "alaw", "ALAW":
			bytesPerSample = 1
		default:
			return 0
		}
	}
	if bytesPerSample <= 0 {
		return 0
	}
	bytesPerFrame := numChans * bytesPerSample
	if bytesPerFrame <= 0 {
		return 0
	}
	d := float64(ssndPCMBytes) / (float64(bytesPerFrame) * sampleRate)
	if math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 {
		return 0
	}
	return d
}

// getAIFFDuration 解析 AIFF 文件头与实际 SSND 音频载荷长度以获取时长。
func getAIFFDuration(r io.ReadSeeker) (float64, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, errors.Wrap(err, "failed to seek aiff file")
	}

	dec := aiff.NewDecoder(r)
	if !dec.IsValidFile() {
		return 0, errors.New("invalid aiff file")
	}

	d, err := dec.Duration()
	if err != nil {
		return 0, errors.Wrap(err, "failed to get aiff duration")
	}

	headerSec := d.Seconds()
	pcmSec := scanAIFFPCMDuration(r)
	if pcmSec > headerSec {
		return pcmSec, nil
	}
	return headerSec, nil
}

// getWebMDuration 解析 WebM 文件以获取时长。
// WebM 使用 Matroska 容器格式
func getWebMDuration(r io.ReadSeeker) (float64, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, errors.Wrap(err, "failed to seek webm file")
	}

	// WebM/Matroska 文件的解析比较复杂
	// 这里提供一个简化的实现，读取 EBML 头部
	// 对于完整的 WebM 解析，可能需要使用专门的库

	// 简单实现：查找 Duration 元素
	// WebM Duration 的 Element ID 是 0x4489
	// 这是一个简化版本，可能不适用于所有 WebM 文件
	buf := make([]byte, 8192)
	n, err := r.Read(buf)
	if err != nil && err != io.EOF {
		return 0, errors.Wrap(err, "failed to read webm file")
	}

	// 尝试查找 Duration 元素（这是一个简化的方法）
	// 实际的 WebM 解析需要完整的 EBML 解析器
	// 这里返回错误，建议使用专门的库
	if n > 0 {
		// 检查 EBML 标识
		if len(buf) >= 4 && binary.BigEndian.Uint32(buf[0:4]) == 0x1A45DFA3 {
			// 这是一个有效的 EBML 文件
			// 但完整解析需要更复杂的逻辑
			return 0, errors.New("webm duration parsing requires full EBML parser (consider using ffprobe for webm files)")
		}
	}

	return 0, errors.New("failed to parse webm file")
}

// getAACDuration 解析 AAC (ADTS格式) 文件以获取时长。
// 使用 gomedia 库来解析 AAC ADTS 帧
func getAACDuration(r io.ReadSeeker) (float64, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, errors.Wrap(err, "failed to seek aac file")
	}

	// 读取整个文件内容
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, errors.Wrap(err, "failed to read aac file")
	}

	var totalFrames int64
	var sampleRate int

	// 使用 gomedia 的 SplitAACFrame 函数来分割 AAC 帧
	codec.SplitAACFrame(data, func(aac []byte) {
		// 解析 ADTS 头部以获取采样率信息
		if len(aac) >= 7 {
			// 使用 ConvertADTSToASC 来获取音频配置信息
			asc, err := codec.ConvertADTSToASC(aac)
			if err == nil && sampleRate == 0 {
				sampleRate = codec.AACSampleIdxToSample(int(asc.Sample_freq_index))
			}
			totalFrames++
		}
	})

	if sampleRate == 0 || totalFrames == 0 {
		return 0, errors.New("no valid aac frames found")
	}

	// 每个 AAC ADTS 帧包含 1024 个采样
	totalSamples := totalFrames * 1024
	duration := float64(totalSamples) / float64(sampleRate)
	return duration, nil
}
