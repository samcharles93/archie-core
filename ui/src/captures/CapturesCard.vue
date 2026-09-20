<script setup lang="ts">
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

import CapturesList from "./CapturesList.vue";

/** The captured events panel, with the note that qualifies everything inside
 * it: what an operator reads here has already been through redaction. */
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Recent captures</CardTitle>
      <CardDescription>Headers and payloads are redacted before they are stored.</CardDescription>
    </CardHeader>
    <CardContent>
      <CapturesList />
    </CardContent>
  </Card>
</template>
