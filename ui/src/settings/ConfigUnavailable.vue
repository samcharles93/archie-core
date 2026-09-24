<script setup lang="ts">
import { Card, CardContent } from "@/components/ui/card";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import { configError } from "./state";

/**
 * What a System page shows in place of its content when there is none to
 * render: a failed read, or archied running without a config file wired into
 * the dashboard. The page renders this instead of its cards rather than beside
 * them -- half a page that quietly omitted the rest would read as the whole.
 */
</script>

<template>
  <Card class="mb-4">
    <CardContent>
      <Empty>
        <EmptyHeader>
          <EmptyTitle>{{
            configError ? "Cannot reach archied" : "No configuration loaded"
          }}</EmptyTitle>
          <EmptyDescription v-if="configError">{{
            configError
          }}</EmptyDescription>
          <EmptyDescription v-else>
            archied is running without a config file wired into the dashboard.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    </CardContent>
  </Card>
</template>
