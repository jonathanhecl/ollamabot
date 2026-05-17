# Local Model Inventory

Generated: 2026-05-16T23:27:50-03:00

Base URL: `http://localhost:11434`

Ollama version: `0.24.0`

| Model | Family | Params | Quant | Context | Capabilities | Encoders |
| --- | --- | --- | --- | ---: | --- | --- |
| `deepseek-ocr:latest` | deepseekocr | 3.3B | F16 | 8192 | completion=comprobado, tools=pendiente, thinking=pendiente, vision=comprobado, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `functiongemma:latest` | gemma3 | 268.10M | Q8_0 | 32768 | completion=comprobado, tools=comprobado, thinking=pendiente, vision=pendiente, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `granite3.2-vision:latest` | granite,clip | 2.5B | Q4_K_M | 16384 | completion=comprobado, tools=comprobado, thinking=pendiente, vision=comprobado, embedding=pendiente, audio=pendiente, video=pendiente | vision |
| `granite4:latest` | granite | 3.4B | Q4_K_M | 131072 | completion=comprobado, tools=comprobado, thinking=pendiente, vision=pendiente, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `hf.co/HauhauCS/Gemma-4-E2B-Uncensored-HauhauCS-Aggressive:Q6_K_P` | gemma4 | 4.65B | unknown | 131072 | completion=comprobado, tools=pendiente, thinking=pendiente, vision=comprobado, embedding=pendiente, audio=inferido, video=pendiente | vision,audio |
| `hf.co/HauhauCS/Gemma-4-E2B-Uncensored-HauhauCS-Aggressive:fixed` | gemma4 | 4.6B | Q6_K | 131072 | completion=comprobado, tools=comprobado, thinking=comprobado, vision=pendiente, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `lfm2.5-thinking:latest` | lfm2 | 1.2B | Q4_K_M | 128000 | completion=comprobado, tools=comprobado, thinking=comprobado, vision=pendiente, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `llama3.2-vision:latest` | mllama | 10.7B | Q4_K_M | 131072 | completion=comprobado, tools=pendiente, thinking=pendiente, vision=comprobado, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `minicpm-v:latest` | qwen2,clip | 7.6B | Q4_0 | 32768 | completion=comprobado, tools=pendiente, thinking=pendiente, vision=comprobado, embedding=pendiente, audio=pendiente, video=pendiente | vision |
| `ministral-3:8b` | mistral3 | 8.9B | Q4_K_M | 262144 | completion=comprobado, tools=comprobado, thinking=pendiente, vision=comprobado, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `mistral:latest` | llama | 7.2B | Q4_0 | 32768 | completion=comprobado, tools=comprobado, thinking=pendiente, vision=pendiente, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `nomic-embed-text:latest` | nomic-bert | 137M | F16 | 2048 | completion=pendiente, tools=pendiente, thinking=pendiente, vision=pendiente, embedding=comprobado, audio=pendiente, video=pendiente | - |
| `phi4:latest` | phi3 | 14.7B | Q4_K_M | 16384 | completion=comprobado, tools=pendiente, thinking=pendiente, vision=pendiente, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `qwen3-vl:4b` | qwen3vl | 4.4B | Q4_K_M | 262144 | completion=comprobado, tools=comprobado, thinking=comprobado, vision=comprobado, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `qwen3.5:2b` | qwen35 | 2.3B | Q8_0 | 262144 | completion=comprobado, tools=comprobado, thinking=comprobado, vision=comprobado, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `qwen3.5:4b` | qwen35 | 4.7B | Q4_K_M | 262144 | completion=comprobado, tools=comprobado, thinking=comprobado, vision=comprobado, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `qwen3.5:9b` | qwen35 | 9.7B | Q4_K_M | 262144 | completion=comprobado, tools=comprobado, thinking=comprobado, vision=comprobado, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `qwen3:8b` | qwen3 | 8.2B | Q4_K_M | 40960 | completion=comprobado, tools=comprobado, thinking=comprobado, vision=pendiente, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `test-gemma4-vision:latest` | clip,gemma4 | 475.73M | F16 | 131072 | completion=comprobado, tools=pendiente, thinking=pendiente, vision=comprobado, embedding=pendiente, audio=inferido, video=pendiente | vision,audio |
| `test-gemma4:latest` | gemma4 | 4.6B | Q6_K | 131072 | completion=comprobado, tools=comprobado, thinking=comprobado, vision=pendiente, embedding=pendiente, audio=pendiente, video=pendiente | - |
| `translategemma:4b` | gemma3 | 4.3B | Q4_K_M | 131072 | completion=comprobado, tools=pendiente, thinking=pendiente, vision=comprobado, embedding=pendiente, audio=pendiente, video=pendiente | - |

## Status Semantics

- `comprobado`: reported by Ollama `/api/show.capabilities` or validated by a probe.
- `inferido`: inferred from model/projector metadata, not yet validated end-to-end.
- `pendiente`: not reported and not locally verified.
