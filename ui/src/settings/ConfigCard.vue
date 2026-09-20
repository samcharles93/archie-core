<script setup lang="ts">
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

/**
 * One titled card of the System surface. Every card on the six pages opens the
 * same way -- a title, and the line that says what it covers -- so it is a
 * component rather than six copies of the same CardHeader markup.
 *
 * The card owns its own bottom margin: the pages stack cards directly, with
 * nothing between them.
 */
defineProps<{ title: string; description?: string }>();
</script>

<template>
  <Card class="mb-4">
    <CardHeader>
      <CardTitle>{{ title }}</CardTitle>
      <CardDescription v-if="description">{{ description }}</CardDescription>
    </CardHeader>
    <CardContent>
      <slot />
    </CardContent>
  </Card>
</template>
