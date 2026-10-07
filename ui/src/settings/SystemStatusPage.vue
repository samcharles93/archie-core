<script setup lang="ts">
import PageHeader from "@/base/PageHeader.vue";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import HealthStatusCard from "./HealthStatusCard.vue";
import ServicesCard from "./ServicesCard.vue";
import UpdateSettingsCard from "./UpdateSettingsCard.vue";
import VersionsCard from "./VersionsCard.vue";
</script>

<template>
  <div>
    <PageHeader title="Status" />

    <Tabs default-value="services" class="space-y-4">
      <TabsList>
        <TabsTrigger value="services">Services</TabsTrigger>
        <TabsTrigger value="health">Health</TabsTrigger>
        <TabsTrigger value="versions">Versions</TabsTrigger>
      </TabsList>
      <TabsContent value="services"><ServicesCard /></TabsContent>
      <TabsContent value="health"><HealthStatusCard /></TabsContent>
      <TabsContent value="versions" class="space-y-4">
        <VersionsCard />
        <UpdateSettingsCard />
      </TabsContent>
    </Tabs>
  </div>
</template>
