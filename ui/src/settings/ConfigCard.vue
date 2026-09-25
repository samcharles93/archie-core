<script setup lang="ts">
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

/**
 * One titled card of the System surface. Every card on the six pages opens the
 * same way, so it is a component rather than six copies of the same CardHeader
 * markup. A description is reserved for context the card cannot show itself.
 *
 * The card owns its own bottom margin: the pages stack cards directly, with
 * nothing between them.
 */
defineProps<{ title?: string; description?: string; edited?: boolean }>();
</script>

<template>
  <Card class="mb-4" :class="edited && 'ring-primary/60'">
    <CardHeader v-if="title || description">
      <CardTitle v-if="title">{{ title }}</CardTitle>
      <CardDescription v-if="description">{{ description }}</CardDescription>
      <CardAction v-if="$slots.action"><slot name="action" /></CardAction>
    </CardHeader>
    <CardContent>
      <slot />
    </CardContent>
  </Card>
</template>
