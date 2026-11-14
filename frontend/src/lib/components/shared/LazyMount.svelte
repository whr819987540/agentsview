<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Scroll container whose viewport decides visibility. Pass null for
     *  the browser viewport. While it is undefined (for example before a
     *  parent's bind:this resolves), nothing is observed yet. */
    root: Element | null | undefined;
    /** Height reserved until the content mounts, so the scroll height
     *  stays close to the final layout. */
    placeholderHeight?: number;
    /** How far outside the root the content starts mounting. */
    rootMargin?: string;
    children: Snippet;
  }

  let {
    root,
    placeholderHeight = 240,
    rootMargin = "600px 0px",
    children,
  }: Props = $props();

  // Without IntersectionObserver (old engines, jsdom) mount right away.
  let mounted = $state(typeof IntersectionObserver === "undefined");
  let placeholder: HTMLDivElement | undefined = $state(undefined);

  $effect(() => {
    if (mounted || !placeholder || root === undefined) return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          mounted = true;
          observer.disconnect();
        }
      },
      { root, rootMargin },
    );
    observer.observe(placeholder);
    return () => observer.disconnect();
  });
</script>

<!-- Mounts its content once it nears the visible area and keeps it mounted
     afterwards, so content far down a long page costs nothing until the
     reader scrolls to it. -->
{#if mounted}
  {@render children()}
{:else}
  <div
    bind:this={placeholder}
    class="lazy-mount-placeholder"
    style:min-height="{placeholderHeight}px"
    aria-hidden="true"
  ></div>
{/if}
