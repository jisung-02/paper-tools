package imgconv

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
)

const maxImagePixels uint64 = 16 * 1024 * 1024

func decodeImage(data []byte) (image.Image, string, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode config: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || uint64(config.Width)*uint64(config.Height) > maxImagePixels {
		return nil, "", fmt.Errorf("image exceeds pixel budget of %d", maxImagePixels)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode: %w", err)
	}
	return img, format, nil
}

// Convert decodes image data and re-encodes it to the specified format.
// format must be one of "png", "jpeg", or "gif" (lowercase).
// For JPEG, jpegQuality is used (defaults to 85 if <= 0).
// Returns the encoded bytes or an error if decoding/encoding fails.
func Convert(data []byte, format string, jpegQuality int) ([]byte, error) {
	img, _, err := decodeImage(data)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	switch format {
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encode png: %w", err)
		}
	case "jpeg":
		quality := jpegQuality
		if quality <= 0 {
			quality = 85
		}
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
	case "gif":
		if err := gif.Encode(&buf, img, &gif.Options{NumColors: 256, Quantizer: gifQuantizer}); err != nil {
			return nil, fmt.Errorf("encode gif: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}

	return buf.Bytes(), nil
}

// Resize decodes image data and scales it down to fit within maxW×maxH,
// preserving aspect ratio, then re-encodes it in the input's own format
// (whichever of "png", "jpeg", or "gif" was detected on decode).
//
// It never upscales: if the image already fits within maxW×maxH it is only
// re-encoded, unchanged. A maxW or maxH of 0 means "unconstrained" on that
// axis, so the image is scaled by the other dimension alone; if both are 0
// the image is left at its original size.
//
// For JPEG output, jpegQuality is used (defaults to 85 if <= 0). GIF input
// is decoded and re-encoded as a single (first) frame — animated GIFs lose
// their animation, same as Convert.
//
// Returns the encoded bytes, the detected/output format name, or an error
// if decoding/encoding fails.
func Resize(data []byte, maxW, maxH int, jpegQuality int) ([]byte, string, error) {
	img, format, err := decodeImage(data)
	if err != nil {
		return nil, "", err
	}

	scaled := scaleToFit(img, maxW, maxH)

	var buf bytes.Buffer

	switch format {
	case "png":
		if err := png.Encode(&buf, scaled); err != nil {
			return nil, "", fmt.Errorf("encode png: %w", err)
		}
	case "jpeg":
		quality := jpegQuality
		if quality <= 0 {
			quality = 85
		}
		if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: quality}); err != nil {
			return nil, "", fmt.Errorf("encode jpeg: %w", err)
		}
	case "gif":
		if err := gif.Encode(&buf, scaled, &gif.Options{NumColors: 256, Quantizer: gifQuantizer}); err != nil {
			return nil, "", fmt.Errorf("encode gif: %w", err)
		}
	default:
		return nil, "", fmt.Errorf("unsupported format: %s", format)
	}

	return buf.Bytes(), format, nil
}

// scaleToFit returns img scaled down to fit within maxW×maxH while
// preserving aspect ratio. It never upscales: if img already fits (or
// maxW/maxH are both 0), img is returned unchanged. A maxW or maxH of 0
// means that axis is unconstrained.
func scaleToFit(img image.Image, maxW, maxH int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return img
	}

	scale := 1.0
	if maxW > 0 && w > maxW {
		if s := float64(maxW) / float64(w); s < scale {
			scale = s
		}
	}
	if maxH > 0 && h > maxH {
		if s := float64(maxH) / float64(h); s < scale {
			scale = s
		}
	}
	if scale >= 1.0 {
		return img
	}

	newW := int(float64(w)*scale + 0.5)
	newH := int(float64(h)*scale + 0.5)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	return boxResize(img, newW, newH)
}

// boxResize scales src to exactly newW×newH by box averaging: each
// destination pixel is the plain average (in straight, non-premultiplied
// 8-bit sRGB space) of the block of source pixels that maps to it.
//
// This is a hand-rolled resampler because the standard library has no
// interpolated scaler — image/draw only offers nearest-neighbor copies via
// draw.Draw. Box averaging is exact and alias-free for integer downscale
// ratios (e.g. 4x4 -> 2x2, where each destination pixel is the average of a
// clean 2x2 block) and a reasonable approximation for non-integer ratios,
// where block boundaries are rounded to the nearest source pixel rather
// than area-weighted. That ceiling is acceptable for a thumbnail-style
// resize; it is not a general-purpose, gamma-correct resampler.
func boxResize(src image.Image, newW, newH int) *image.NRGBA {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()

	dst := image.NewNRGBA(image.Rect(0, 0, newW, newH))

	for dy := 0; dy < newH; dy++ {
		y0 := dy * srcH / newH
		y1 := (dy + 1) * srcH / newH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > srcH {
			y1 = srcH
		}

		for dx := 0; dx < newW; dx++ {
			x0 := dx * srcW / newW
			x1 := (dx + 1) * srcW / newW
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > srcW {
				x1 = srcW
			}

			var rSum, gSum, bSum, aSum, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := color.NRGBAModel.Convert(src.At(b.Min.X+sx, b.Min.Y+sy)).(color.NRGBA)
					rSum += uint64(c.R)
					gSum += uint64(c.G)
					bSum += uint64(c.B)
					aSum += uint64(c.A)
					n++
				}
			}

			dst.SetNRGBA(dx, dy, color.NRGBA{
				R: uint8(rSum / n),
				G: uint8(gSum / n),
				B: uint8(bSum / n),
				A: uint8(aSum / n),
			})
		}
	}

	return dst
}
