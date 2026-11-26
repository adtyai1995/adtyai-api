# Channel Availability

ADTY AI provides real-time route observability on the **Channel Status** page so you can verify model health before deploying workloads or choosing a model group for a new API key.

Visit the live status board at [https://www.adtyai.com/model-status](https://www.adtyai.com/model-status).

## Reading the Channel Status Dashboard

Each tracked model and channel entry displays three core health signals:

### 1. 24-Hour Heartbeat Bars

A sequence of vertical time-slice bars summarizes automated probe checks over the past 24 hours from left (oldest) to right (most recent):

- **Green**: Probes succeeded within normal latency bounds.
- **Amber / Yellow**: Partial degradation, intermittent retries, or elevated response time during that interval.
- **Red**: Consecutive probe failures or upstream outage in that window.
- **Gray / Empty**: No probe data recorded for that slice (for example, a newly added route).

Hovering over any bar reveals the timestamp window and detailed probe metrics.

### 2. Latency Metrics

Latency reflects how quickly the upstream route responds during health checks. Comparing latency across concrete groups helps you decide between ultra-responsive interactive coding routes (such as Claude Code or Codex CLI) and cost-optimized batch routes.

### 3. Success Rate

Rolling success rate indicates the percentage of recent health checks that completed cleanly without upstream `5xx` errors or timeouts. For production automation and long-running agent sessions, prioritize groups with consistently high 24-hour success rates.

## Choosing a Concrete Model Group for Your API Key

On ADTY AI, every `sk-...` API key is bound to a **concrete model group** that you choose when creating the key. A model group defines:

- Which upstream channel pool serves your requests
- Which models are accessible to that key
- The pricing multiplier applied to token and image billing

### Recommended Selection Workflow

1. **Check the Pricing page** in the console to compare available concrete model groups, their supported model lists, and their billing multipliers.
2. **Cross-check [https://www.adtyai.com/model-status](https://www.adtyai.com/model-status)** to inspect the 24-hour heartbeat bars, latency, and success rate of the models you plan to call in that group.
3. **Create a dedicated API key** assigned to that specific model group.
4. **Verify available models** after creating the key by calling `GET https://api.adtyai.com/v1/models` (see [API Reference](/api/)).

::: tip Switching Model Groups Later
If your workload needs shift—for example, moving from a budget group to a high-throughput priority group—simply create a new API key bound to the target concrete model group (or update the key's group in the console if permitted) and update your client configuration.
:::

## Troubleshooting Degraded Routes

If you notice elevated latency or `502`/`503`/`504` responses in your client:

1. Check [https://www.adtyai.com/model-status](https://www.adtyai.com/model-status) to see whether an upstream provider is experiencing a regional or model-specific incident.
2. Check whether another concrete model group offering the same model shows green heartbeats and lower latency.
3. Copy the **Request ID** from **Usage Logs** if errors persist on a healthy-looking channel, and report it via the [Support Center](/guides/support).
